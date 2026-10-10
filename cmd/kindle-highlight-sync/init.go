package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/config"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/exitcode"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/notion"
	"github.com/ryokosuge/kindle-highlight-sync-cli/internal/secrets"
)

const initRegion = "japan"

// newNotionClient は init と sync が使う Notion クライアントを作る。テストは httptest のサーバーに向けて差し替える。
var newNotionClient = func(token string) *notion.Client { return notion.New(token) }

// newInitPrompter は init の対話入力を作る。テストは入力を差し替える。
var newInitPrompter = newPrompter

var mappingFields = []struct {
	field notion.Field
	value func(*config.Mapping) *string
}{ // internal/notion/mapping.go の fields と同じ順
	{notion.FieldTitle, func(m *config.Mapping) *string { return &m.Title }},
	{notion.FieldKindleID, func(m *config.Mapping) *string { return &m.KindleID }},
	{notion.FieldAuthor, func(m *config.Mapping) *string { return &m.Author }},
	{notion.FieldASIN, func(m *config.Mapping) *string { return &m.ASIN }},
	{notion.FieldLastAnnotatedAt, func(m *config.Mapping) *string { return &m.LastAnnotatedAt }},
}

func newInitCmd(a *app) *cobra.Command {
	var force, yes bool
	var databaseID string
	cmd := &cobra.Command{
		Use:         "init",
		Short:       "Notion token・Books Database・Property の対応を確かめて config.toml を作る",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{ignoreConfigErrorAnnotation: "true"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, a, databaseID, force, yes)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "既存の config.toml を作り直す")
	cmd.Flags().StringVar(&databaseID, "database-id", "", "Notion Database の ID（--yes では必須）")
	cmd.Flags().BoolVar(&yes, "yes", false, "対話せず既定値で進める")
	return cmd
}

func runInit(cmd *cobra.Command, a *app, databaseID string, force, yes bool) error {
	if err := initExistingConfig(a.paths.ConfigFile, force); err != nil {
		return exitcode.New(exitcode.ConfigRequired, err)
	}
	if yes && databaseID == "" {
		return exitcode.New(exitcode.ConfigRequired, errors.New("--yes requires --database-id"))
	}

	var p *prompter
	if !yes {
		p = newInitPrompter(cmd)
	}
	token, saveToken, err := initToken(p, yes)
	if err != nil {
		return err
	}

	id := databaseID
	if id == "" {
		id, err = p.line("Notion Database URL or ID")
		if err != nil {
			return err
		}
	}
	id, err = notion.ParseDatabaseID(id)
	if err != nil {
		return exitcode.New(exitcode.ConfigRequired, err)
	}

	client := newNotionClient(token)
	ref, err := initDataSource(cmd, client, id, p, yes)
	if err != nil {
		return err
	}
	ds, err := client.RetrieveDataSource(cmd.Context(), ref.ID)
	if err != nil {
		return err
	}
	mapping, err := initMapping(cmd.ErrOrStderr(), p, ds, yes)
	if err != nil {
		return err
	}

	if err := initExistingConfig(a.paths.ConfigFile, force); err != nil {
		return exitcode.New(exitcode.ConfigRequired, err)
	}

	var previousToken string
	hasPreviousToken := false
	if saveToken {
		var err error
		previousToken, _, err = secrets.NotionToken()
		if err != nil && !errors.Is(err, secrets.ErrNotFound) {
			return err
		}
		hasPreviousToken = err == nil
		if err := secrets.SaveNotionToken(token); err != nil {
			return err
		}
	}
	cfg := &config.Config{
		Amazon: config.Amazon{Region: initRegion},
		Notion: config.Notion{
			DatabaseID:   id,
			DataSourceID: ref.ID,
			Mapping:      mapping,
		},
	}
	if err := config.Save(a.paths.ConfigFile, cfg); err != nil {
		if hasPreviousToken {
			if rbErr := secrets.SaveNotionToken(previousToken); rbErr != nil {
				return errors.Join(err, fmt.Errorf("restore previous Notion token: %w", rbErr))
			}
		}
		return err
	}
	printInitSummary(cmd, a.paths.ConfigFile, id, ref.Name, mapping, ds)
	return nil
}

func initExistingConfig(path string, force bool) error {
	_, err := os.Stat(path)
	switch {
	case err == nil && !force:
		return fmt.Errorf("%s already exists: run init --force to recreate it", path)
	case err == nil, errors.Is(err, fs.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("stat %s: %w", path, err)
	}
}

func initToken(p *prompter, yes bool) (token string, save bool, err error) {
	if yes {
		token, _, err := secrets.NotionToken()
		if errors.Is(err, secrets.ErrNotFound) {
			return "", false, exitcode.New(exitcode.ConfigRequired, err)
		}
		return token, false, err
	}
	if token = os.Getenv(secrets.EnvNotionToken); token != "" {
		return token, false, nil
	}
	token, err = p.secret("Notion token")
	if err != nil {
		return "", false, err
	}
	if token == "" {
		return "", false, exitcode.New(exitcode.ConfigRequired, errors.New("notion token must not be empty"))
	}
	return token, true, nil
}

func initDataSource(cmd *cobra.Command, client *notion.Client, id string, p *prompter, yes bool) (notion.DataSourceRef, error) {
	db, err := client.RetrieveDatabase(cmd.Context(), id)
	if err != nil {
		return notion.DataSourceRef{}, err
	}
	switch len(db.DataSources) {
	case 0:
		return notion.DataSourceRef{}, exitcode.New(exitcode.ConfigRequired, errors.New("database has no data source"))
	case 1:
		return db.DataSources[0], nil
	}
	if yes {
		return notion.DataSourceRef{}, exitcode.New(exitcode.ConfigRequired, fmt.Errorf("database has %d data sources: run init without --yes to choose one", len(db.DataSources)))
	}
	names := make([]string, len(db.DataSources))
	for i, ref := range db.DataSources {
		names[i] = ref.Name
	}
	i, err := p.choose("Data Source", names, noDefault, false)
	if err != nil {
		return notion.DataSourceRef{}, err
	}
	return db.DataSources[i], nil
}

func initMapping(w io.Writer, p *prompter, ds *notion.DataSource, yes bool) (config.Mapping, error) {
	check := notion.CheckMapping(ds.Properties)
	if err := check.Err(); err != nil {
		return config.Mapping{}, err
	}
	for _, line := range check.SkippedLines() {
		_, _ = fmt.Fprintln(w, line)
	}

	defaults, missing := notion.DefaultMapping(ds.Properties)
	if yes {
		if len(missing) > 0 {
			names := make([]string, len(missing))
			for i, field := range missing {
				names[i] = string(field)
			}
			return config.Mapping{}, exitcode.New(exitcode.ConfigRequired, fmt.Errorf("no default property for %s: run init without --yes to choose", strings.Join(names, ", ")))
		}
		return defaults, nil
	}

	candidates := notion.Candidates(ds.Properties)
	var mapping config.Mapping
	for _, entry := range mappingFields {
		options := candidates[entry.field]
		def := noDefault
		optional := !entry.field.Required()
		if optional {
			def = notMapped
		}
		if id := *entry.value(&defaults); id != "" {
			for i, option := range options {
				if option.ID == id {
					def = i
					break
				}
			}
		}
		names := make([]string, len(options))
		for i, option := range options {
			names[i] = option.Name
		}
		i, err := p.choose(string(entry.field), names, def, optional)
		if errors.Is(err, errNoOptions) {
			return config.Mapping{}, exitcode.New(exitcode.ConfigRequired, fmt.Errorf("%s: %w", entry.field, err))
		}
		if err != nil {
			return config.Mapping{}, err
		}
		if i != notMapped {
			*entry.value(&mapping) = options[i].ID
		}
	}
	return mapping, nil
}

func printInitSummary(cmd *cobra.Command, path, databaseID, dataSourceName string, mapping config.Mapping, ds *notion.DataSource) {
	propertyName := func(id string) string {
		for _, prop := range ds.Properties {
			if prop.ID == id {
				return prop.Name
			}
		}
		return "(not mapped)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "saved %s\n", path)
	fmt.Fprintf(&b, "database_id: %s\n", databaseID)
	fmt.Fprintf(&b, "data source: %s\n", dataSourceName)
	for _, entry := range mappingFields {
		fmt.Fprintf(&b, "%s: %s\n", entry.field, propertyName(*entry.value(&mapping)))
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), b.String())
}
