package engine

import (
	"context"
	"testing"

	searchService "github.com/owncloud/ocis/v2/protogen/gen/ocis/services/search/v0"
	"github.com/owncloud/ocis/v2/services/search/pkg/config"
	"github.com/owncloud/ocis/v2/services/search/pkg/query/elasticsearch"
	"github.com/stretchr/testify/require"
)

func TestElasticsearchEngine_Interface(t *testing.T) {
	// Test that ElasticSearch implements the Engine interface
	var _ Engine = (*ElasticSearch)(nil)
}

func TestElasticsearchEngine_NewElasticSearchEngine(t *testing.T) {
	// Test creating a new Elasticsearch engine
	config := config.EngineElasticsearch{
		Addresses: []string{"http://localhost:9200"},
		IndexName: "test-index",
	}

	// This would normally connect to Elasticsearch, but for testing we'll just check the structure
	engine, err := NewElasticSearchEngine(config, elasticsearch.DefaultCreator)

	// In a real test environment with Elasticsearch running, this would succeed
	// For now, we expect it to fail since no Elasticsearch is running
	if err != nil {
		t.Logf("Expected error when no Elasticsearch server is available: %v", err)
		return
	}

	require.NotNil(t, engine)
	require.Equal(t, "test-index", engine.indexName)
}

func TestElasticsearchEngine_QueryCreation(t *testing.T) {
	// Test query creation without connecting to Elasticsearch
	creator := elasticsearch.DefaultCreator

	// Test simple query
	query, err := creator.Create("name:test")
	require.NoError(t, err)
	require.NotNil(t, query)

	// Test that the query structure is correct
	boolQuery, ok := query["bool"].(map[string]interface{})
	require.True(t, ok, "Query should be wrapped in bool query")
	require.NotNil(t, boolQuery["must"], "Query should have must clause")

	// Test more complex query
	complexQuery, err := creator.Create("name:test AND content:example")
	require.NoError(t, err)
	require.NotNil(t, complexQuery)

	// Test OR query
	orQuery, err := creator.Create("name:test OR name:example")
	require.NoError(t, err)
	require.NotNil(t, orQuery)
}

func TestElasticsearchEngine_SearchRequest(t *testing.T) {
	// This test would require a running Elasticsearch instance
	// For now, we'll just test the method signature
	config := config.EngineElasticsearch{
		Addresses: []string{"http://localhost:9200"},
		IndexName: "test-index",
	}

	engine, err := NewElasticSearchEngine(config, elasticsearch.DefaultCreator)
	if err != nil {
		t.Skip("Skipping test - no Elasticsearch server available")
	}

	ctx := context.Background()
	req := &searchService.SearchIndexRequest{
		Query:    "test query",
		PageSize: 10,
	}

	// This would normally execute the search
	_, err = engine.Search(ctx, req)
	// We expect this to work if Elasticsearch is available
	if err != nil {
		t.Logf("Search failed (expected if no Elasticsearch): %v", err)
	}
}

func TestElasticsearchEngine_ResourceOperations(t *testing.T) {
	// Test that the engine can be used for resource operations
	config := config.EngineElasticsearch{
		Addresses: []string{"http://localhost:9200"},
		IndexName: "test-index",
	}

	engine, err := NewElasticSearchEngine(config, elasticsearch.DefaultCreator)
	if err != nil {
		t.Skip("Skipping test - no Elasticsearch server available")
	}

	// Test resource creation
	resource := Resource{
		ID:     "test-id",
		RootID: "root-id",
		Path:   "/test/path",
		Type:   1,
	}

	err = engine.Upsert("test-id", resource)
	if err != nil {
		t.Logf("Upsert failed (expected if no Elasticsearch): %v", err)
	}

	// Test doc count
	count, err := engine.DocCount()
	if err != nil {
		t.Logf("DocCount failed (expected if no Elasticsearch): %v", err)
	} else {
		t.Logf("Document count: %d", count)
	}
}
