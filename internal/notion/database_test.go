package notion

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestRetrieveDatabase(t *testing.T) {
	t.Run("未知フィールドを無視して取得する", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"object":"database",
				"id":"database-id",
				"title":[{"type":"text","text":{"content":"Books"}}],
				"parent":{"type":"workspace","workspace":true},
				"is_inline":false,
				"data_sources":[
					{"id":"source-1","name":"Books","created_time":"2026-03-11"},
					{"id":"source-2","name":"Archive","extra":true}
				]
			}`))
		})

		got, err := client.RetrieveDatabase(context.Background(), "database-id")
		if err != nil {
			t.Fatalf("RetrieveDatabase() error = %v", err)
		}
		want := &Database{
			ID: "database-id",
			DataSources: []DataSourceRef{
				{ID: "source-1", Name: "Books"},
				{ID: "source-2", Name: "Archive"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("RetrieveDatabase() = %#v, want %#v", got, want)
		}
	})

	t.Run("空の data_sources を取得する", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"database","id":"db","data_sources":[]}`))
		})
		got, err := client.RetrieveDatabase(context.Background(), "db")
		if err != nil {
			t.Fatalf("RetrieveDatabase() error = %v", err)
		}
		if len(got.DataSources) != 0 {
			t.Errorf("len(DataSources) = %d, want 0", len(got.DataSources))
		}
	})

	t.Run("空の名前は有効", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"database","id":"db","data_sources":[{"id":"ds","name":""}]}`))
		})
		got, err := client.RetrieveDatabase(context.Background(), "db")
		if err != nil {
			t.Fatalf("RetrieveDatabase() error = %v", err)
		}
		if got.DataSources[0].Name != "" {
			t.Errorf("DataSources[0].Name = %q, want empty string", got.DataSources[0].Name)
		}
	})

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "object が異なる", body: `{"object":"page","id":"db","data_sources":[]}`, want: "object"},
		{name: "id がない", body: `{"object":"database","data_sources":[]}`, want: "id"},
		{name: "data_sources がない", body: `{"object":"database","id":"db"}`, want: "data_sources"},
		{name: "data_sources が null", body: `{"object":"database","id":"db","data_sources":null}`, want: "data_sources"},
		{name: "要素の id がない", body: `{"object":"database","id":"db","data_sources":[{"name":"Books"}]}`, want: "data_sources"},
		{name: "要素の name がない", body: `{"object":"database","id":"db","data_sources":[{"id":"ds"}]}`, want: "data_sources"},
		{name: "要素の name が null", body: `{"object":"database","id":"db","data_sources":[{"id":"ds","name":null}]}`, want: "data_sources"},
		{name: "id が重複する", body: `{"object":"database","id":"db","data_sources":[{"id":"ds","name":"A"},{"id":"ds","name":"B"}]}`, want: "data_sources"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := client.RetrieveDatabase(context.Background(), "db")
			assertNotionError(t, err, tt.want)
		})
	}
}

func TestRetrieveDataSource(t *testing.T) {
	t.Run("Property を名前順に取得する", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{
				"object":"data_source",
				"id":"source-id",
				"properties":{
					"Name":{"id":"title","name":"Name","description":null,"type":"title","title":{}},
					"Author":{"id":"%3Axs","name":"Author","description":null,"type":"rich_text","rich_text":{}},
					"Last Annotated":{"id":"Ef56","name":"Last Annotated","description":null,"type":"date","date":{}}
				},
				"created_time":"2026-03-11",
				"parent":{"type":"database_id"}
			}`))
		})

		got, err := client.RetrieveDataSource(context.Background(), "source-id")
		if err != nil {
			t.Fatalf("RetrieveDataSource() error = %v", err)
		}
		want := &DataSource{
			ID: "source-id",
			Properties: []Property{
				{ID: "%3Axs", Name: "Author", Type: "rich_text"},
				{ID: "Ef56", Name: "Last Annotated", Type: "date"},
				{ID: "title", Name: "Name", Type: "title"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("RetrieveDataSource() = %#v, want %#v", got, want)
		}
	})

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "object が異なる", body: `{"object":"database","id":"ds","properties":{}}`, want: "object"},
		{name: "id がない", body: `{"object":"data_source","properties":{}}`, want: "id"},
		{name: "properties がない", body: `{"object":"data_source","id":"ds"}`, want: "properties"},
		{name: "properties が null", body: `{"object":"data_source","id":"ds","properties":null}`, want: "properties"},
		{name: "Property の id がない", body: `{"object":"data_source","id":"ds","properties":{"Name":{"name":"Name","type":"title"}}}`, want: "id"},
		{name: "Property の id が空", body: `{"object":"data_source","id":"ds","properties":{"Name":{"id":"","name":"Name","type":"title"}}}`, want: "id"},
		{name: "Property の name がない", body: `{"object":"data_source","id":"ds","properties":{"Name":{"id":"n","type":"title"}}}`, want: "name"},
		{name: "Property の type がない", body: `{"object":"data_source","id":"ds","properties":{"Name":{"id":"n","name":"Name"}}}`, want: "type"},
		{name: "Property の type が null", body: `{"object":"data_source","id":"ds","properties":{"Name":{"id":"n","name":"Name","type":null}}}`, want: "type"},
		{name: "Property の type が空", body: `{"object":"data_source","id":"ds","properties":{"Name":{"id":"n","name":"Name","type":""}}}`, want: "type"},
		{name: "key と name が異なる", body: `{"object":"data_source","id":"ds","properties":{"Name":{"id":"n","name":"Title","type":"title"}}}`, want: "does not match key"},
		{name: "Property の id が重複する", body: `{"object":"data_source","id":"ds","properties":{"Author":{"id":"same","name":"Author","type":"rich_text"},"Name":{"id":"same","name":"Name","type":"title"}}}`, want: "duplicate id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			})
			_, err := client.RetrieveDataSource(context.Background(), "ds")
			assertNotionError(t, err, tt.want)
		})
	}

	t.Run("空の properties を取得する", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"data_source","id":"ds","properties":{}}`))
		})
		got, err := client.RetrieveDataSource(context.Background(), "ds")
		if err != nil {
			t.Fatalf("RetrieveDataSource() error = %v", err)
		}
		if len(got.Properties) != 0 {
			t.Errorf("len(Properties) = %d, want 0", len(got.Properties))
		}
	})

	t.Run("未知の Property フィールドを無視する", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"data_source","id":"ds","properties":{"Name":{"id":"title","name":"Name","type":"title","title":{},"extra":"value"}}}`))
		})
		got, err := client.RetrieveDataSource(context.Background(), "ds")
		if err != nil {
			t.Fatalf("RetrieveDataSource() error = %v", err)
		}
		if len(got.Properties) != 1 || got.Properties[0] != (Property{ID: "title", Name: "Name", Type: "title"}) {
			t.Errorf("RetrieveDataSource() Properties = %#v", got.Properties)
		}
	})

	t.Run("エラーには Property 名が含まれる", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"data_source","id":"ds","properties":{"Visible":{"id":"p","name":"Hidden","type":"title"}}}`))
		})
		_, err := client.RetrieveDataSource(context.Background(), "ds")
		assertNotionError(t, err, "Visible", "Hidden")
		if !strings.Contains(err.Error(), "does not match key") {
			t.Errorf("error = %q, want key mismatch", err)
		}
	})
}
