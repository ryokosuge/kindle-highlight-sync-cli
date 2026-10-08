package notion

import (
	"context"
	"net/http"
	"net/url"
	"sort"
)

// Database は Retrieve a database の応答のうち init が使う部分。
type Database struct {
	ID          string
	DataSources []DataSourceRef
}

// DataSourceRef は Database 配下の Data Source の ID と名前。
type DataSourceRef struct{ ID, Name string }

// DataSource は Retrieve a data source の応答のうち init が使う部分。
type DataSource struct {
	ID         string
	Properties []Property
}

// Property は Data Source の Property の ID・名前・型。ID は Notion が返す URL エンコード済みの値のまま。
type Property struct{ ID, Name, Type string }

type databaseResponse struct {
	Object      *string `json:"object"`
	ID          *string `json:"id"`
	DataSources *[]struct {
		ID   *string `json:"id"`
		Name *string `json:"name"`
	} `json:"data_sources"`
}

type dataSourceResponse struct {
	Object     *string `json:"object"`
	ID         *string `json:"id"`
	Properties *map[string]struct {
		ID   *string `json:"id"`
		Name *string `json:"name"`
		Type *string `json:"type"`
	} `json:"properties"`
}

// RetrieveDatabase は GET /v1/databases/{id} を呼ぶ。失敗はすべて exitcode.Notion。
func (c *Client) RetrieveDatabase(ctx context.Context, databaseID string) (*Database, error) {
	path := "/v1/databases/" + url.PathEscape(databaseID)
	var response databaseResponse
	if err := c.get(ctx, path, &response); err != nil {
		return nil, err
	}
	if response.Object == nil {
		return nil, c.invalidResponse(http.MethodGet, path, "object is required")
	}
	if *response.Object != "database" {
		return nil, c.invalidResponse(http.MethodGet, path, "object must be database")
	}
	if response.ID == nil {
		return nil, c.invalidResponse(http.MethodGet, path, "id is required")
	}
	if *response.ID == "" {
		return nil, c.invalidResponse(http.MethodGet, path, "id must not be empty")
	}
	if response.DataSources == nil {
		return nil, c.invalidResponse(http.MethodGet, path, "data_sources is required")
	}

	database := &Database{ID: *response.ID}
	seen := make(map[string]struct{}, len(*response.DataSources))
	for i, ref := range *response.DataSources {
		if ref.ID == nil {
			return nil, c.invalidResponse(http.MethodGet, path, "data_sources[%d].id is required", i)
		}
		if *ref.ID == "" {
			return nil, c.invalidResponse(http.MethodGet, path, "data_sources[%d].id must not be empty", i)
		}
		if ref.Name == nil {
			return nil, c.invalidResponse(http.MethodGet, path, "data_sources[%d].name is required", i)
		}
		if _, ok := seen[*ref.ID]; ok {
			return nil, c.invalidResponse(http.MethodGet, path, "data_sources contains duplicate id")
		}
		seen[*ref.ID] = struct{}{}
		database.DataSources = append(database.DataSources, DataSourceRef{ID: *ref.ID, Name: *ref.Name})
	}
	return database, nil
}

// RetrieveDataSource は GET /v1/data_sources/{id} を呼ぶ。Properties は Name の昇順。
func (c *Client) RetrieveDataSource(ctx context.Context, dataSourceID string) (*DataSource, error) {
	path := "/v1/data_sources/" + url.PathEscape(dataSourceID)
	var response dataSourceResponse
	if err := c.get(ctx, path, &response); err != nil {
		return nil, err
	}
	if response.Object == nil {
		return nil, c.invalidResponse(http.MethodGet, path, "object is required")
	}
	if *response.Object != "data_source" {
		return nil, c.invalidResponse(http.MethodGet, path, "object must be data_source")
	}
	if response.ID == nil {
		return nil, c.invalidResponse(http.MethodGet, path, "id is required")
	}
	if *response.ID == "" {
		return nil, c.invalidResponse(http.MethodGet, path, "id must not be empty")
	}
	if response.Properties == nil {
		return nil, c.invalidResponse(http.MethodGet, path, "properties is required")
	}

	dataSource := &DataSource{ID: *response.ID}
	keys := make([]string, 0, len(*response.Properties))
	for key := range *response.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		property := (*response.Properties)[key]
		if property.ID == nil {
			return nil, c.invalidResponse(http.MethodGet, path, "properties[%q].id is required", key)
		}
		if *property.ID == "" {
			return nil, c.invalidResponse(http.MethodGet, path, "properties[%q].id must not be empty", key)
		}
		if property.Name == nil {
			return nil, c.invalidResponse(http.MethodGet, path, "properties[%q].name is required", key)
		}
		if property.Type == nil {
			return nil, c.invalidResponse(http.MethodGet, path, "properties[%q].type is required", key)
		}
		if *property.Type == "" {
			return nil, c.invalidResponse(http.MethodGet, path, "properties[%q].type must not be empty", key)
		}
		if key != *property.Name {
			return nil, c.invalidResponse(http.MethodGet, path, "properties[%q]: name %q does not match key", key, *property.Name)
		}
		if _, ok := seen[*property.ID]; ok {
			return nil, c.invalidResponse(http.MethodGet, path, "properties contains duplicate id")
		}
		seen[*property.ID] = struct{}{}
		dataSource.Properties = append(dataSource.Properties, Property{
			ID:   *property.ID,
			Name: *property.Name,
			Type: *property.Type,
		})
	}
	sort.Slice(dataSource.Properties, func(i, j int) bool {
		return dataSource.Properties[i].Name < dataSource.Properties[j].Name
	})
	return dataSource, nil
}
