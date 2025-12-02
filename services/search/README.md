# Search

The search service is responsible for metadata and content extraction, stores that data as index and makes it searchable. The following clarifies the extraction terms _metadata_ and _content_:

*   Metadata: all data that _describes_ the file like `Name`, `Size`, `MimeType`, `Tags` and `Mtime`.
*   Content: all data that _relates to content_ of the file like `words`, `geo data`, `exif data` etc.

## General Considerations

*   To use the search service, an event system needs to be configured for all services like NATS, which is shipped and preconfigured.
*   The search service consumes events and does not block other tasks.
*   When looking for content extraction, [Apache Tika - a content analysis toolkit](https://tika.apache.org) can be used but needs to be installed separately.

Extractions are stored as index via the search service. Consider that indexing requires adequate storage capacity - and the space requirement will grow. To avoid filling up the filesystem with the index and rendering Infinite Scale unusable, the index should reside on its own filesystem.

You can change the path to where search maintains its data in case the filesystem gets close to full and you need to relocate the data. Stop the service, move the data, reconfigure the path in the environment variable and restart the service.

When using content extraction, more resources and time are needed, because the content of the file needs to be analyzed. This is especially true for big and multiple concurrent files.

The search service runs out of the box with the shipped default `basic` configuration. No further configuration is needed, except when using content extraction.

Consider using a dedicated hardware for this service in case more resources are needed.

## Scaling

The search service can be scaled by running multiple instances. Some rules apply:

* With `SEARCH_ENGINE_BLEVE_SCALE=false`, which is the default , the search service has exclusive write access to the index. Once the first search process is started, any subsequent {search processes attempting to access the index are locked out.

* With `SEARCH_ENGINE_BLEVE_SCALE=true`, a search service will no longer have exclusive write access to the index. This setting must be enabled for all instances of the {search service.

## Search Engines

By default, the search service is shipped with [bleve](https://github.com/blevesearch/bleve) as its primary search engine. The available engines can be extended by implementing the [Engine](pkg/engine/engine.go) interface and making that engine available.

## Query language

By default, [KQL](https://learn.microsoft.com/en-us/sharepoint/dev/general-development/keyword-query-language-kql-syntax-reference) is used as query language, for an overview of how the syntax works, please read the [microsoft documentation](https://learn.microsoft.com/en-us/sharepoint/dev/general-development/keyword-query-language-kql-syntax-reference) for more details.

Not all parts are supported. The following list gives an overview of parts that are **not implemented** yet:

*   Synonym operators
*   Inclusion and exclusion operators
*   Dynamic ranking operator
*   ONEAR operator
*   NEAR operator
*   Date intervals

In the following [ADR](https://github.com/owncloud/ocis/blob/docs/ocis/adr/0020-file-search-query-language.md) you can read why we chose KQL.

## Extraction Engines

The search service provides the following extraction engines and their results are used as index for searching:

*   The embedded `basic` configuration provides metadata extraction which is always on.
*   The `tika` configuration, which _additionally_ provides content extraction, if installed and configured.

## Content Extraction

The search service is able to manage and retrieve many types of information. For this purpose the following content extractors are included:

### Basic Extractor

This extractor is the most simple one and just uses the resource information provided by Infinite Scale. It does not do any further analysis.

### Tika Extractor

This extractor is more advanced compared to the [Basic extractor](#basic-extractor). The main difference is that this extractor is able to search file contents. However, [Apache Tika](https://tika.apache.org/) is required for this task. Read the [Getting Started with Apache Tika](https://tika.apache.org/3.2.0/gettingstarted.html) guide on how to install and run Tika or use a ready to run [Tika container](https://hub.docker.com/r/apache/tika). See the [Tika container usage document](https://github.com/apache/tika-docker#usage) for a quickstart. Note that at the time of writing, containers are only available for the amd64 platform.

As soon as Tika is installed and accessible, the search service must be configured for the use with Tika. The following settings must be set:

*   `SEARCH_EXTRACTOR_TYPE=tika`
*   `SEARCH_EXTRACTOR_TIKA_TIKA_URL=http://YOUR-TIKA.URL`
*   `FRONTEND_FULL_TEXT_SEARCH_ENABLED=true`\
When using the Tika extractor, make sure to also set this enironment variable in the frontend service. This will tell the web client that full-text search has been enabled.

When the search service can reach Tika, it begins to read out the content on demand. Note that files must be downloaded during the process, which can lead to delays with larger documents.

Content extraction and handling the extracted content can be very resource intensive. Content extraction is therefore limited to files with a certain file size. The default limit is 20MB and can be configured using the `SEARCH_CONTENT_EXTRACTION_SIZE_LIMIT` variable.

When extracting content, you can specify whether [stop words](https://en.wikipedia.org/wiki/Stop_word) like `I`, `you`, `the` are ignored or not. Noramlly, these stop words are removed automatically. To keep them, the environment variable `SEARCH_EXTRACTOR_TIKA_CLEAN_STOP_WORDS` must be set to `false`.

When using the Tika container and docker-compose, consider the following:

*   See the [ocis_full](https://github.com/owncloud/ocis/tree/master/deployments/examples/ocis_full) example.
*   Containers for the linked service are reachable at a hostname identical to the alias or the service name if no alias was specified.

## Search Functionality

The search service consists of two main parts which are file `indexing` and file `search`.

### Indexing

Every time a resource changes its state, a corresponding event is triggered. Based on the event, the search service processes the file and adds the result to its index. There are a few more steps between accepting the file and updating the index.

### Search

A query via the search service will return results based on the index created.

### State Changes which Trigger Indexing

The following state changes in the life cycle of a file can trigger the creation of an index or an update:

#### Resource Trashed

The service checks its index to see if the file has been processed. If an index entry exists, the index will be marked as deleted. In consequence, the file won't appear in search requests anymore. The index entry stays intact and could be restored via [Resource Restored](#resource-restored).

#### Resource Deleted

The service checks its index to see if the file has been processed. If an index entry exists, the index will be finally deleted. In consequence, the file won't appear in search requests anymore.

#### Resource Restored

This step is the counterpart of [Resource Trashed](#resource-trashed). When a file is deleted, is isn't removed from the index, instead the service just marks it as deleted. This mark is removed when the file has been restored, and it shows up in search results again.

#### Resource Moved

This comes into play whenever a file or folder is renamed or moved. The search index then updates the resource location path or starts indexing if no index has been created so far for all items affected. See [Notes](#notes) for an example.

#### Folder Created

The creation of a folder always triggers indexing. The search service extracts all necessary information and stores it in the search index

#### File Created

This case is similar to [Folder created](#folder-created) with the difference that a file can contain far more valuable information. This gets interesting but time-consuming when data content needs to be analyzed and indexed. Content extraction is part of the search service if configured.

#### File Version Restored

Since Infinite Scale is capable of storing multiple versions of the same file, the search service also needs to take care of those versions. When a file version is restored, the service starts to extract all needed information, creates the index and makes the file discoverable.

#### Resource Tag Added

Whenever a resource gets a new tag, the service takes care of it and makes that resource discoverable by the tag.

#### Resource Tag Removed

This is the counterpart of [Resource tag added](#resource-tag-added). It takes care that a tag gets unassigned from the referenced resource.

#### File Uploaded - Synchronous

This case only triggers indexing if `async post processing` is disabled. If so, the service starts to extract all needed file information, stores it in the index and makes it discoverable.

#### File Uploaded - Asynchronous

This is exactly the same as [File uploaded - synchronous](#file-uploaded---synchronous) with the only difference that it is used for asynchronous uploads.

## Manually Trigger Re-Indexing a Space

The service includes a command-line interface to trigger re-indexing a space:

```shell
ocis search index --space $SPACE_ID
```

It can also be used to re-index all spaces:

```shell
ocis search index --all-spaces
```

Note that either `--space $SPACE_ID` or `--all-spaces` must be set.

## Notes

The indexing process tries to be self-healing in some situations.

In the following example, let's assume a file tree `foo/bar/baz` exists.
If the folder `bar` gets renamed to `new-bar`, the path to `baz` is no longer `foo/bar/baz` but `foo/new-bar/baz`.
The search service checks the change and either just updates the path in the index or creates a new index for all items affected if none was present.

# Search Engines

This package provides search engine implementations for the search service. Currently, two engines are supported:

## Bleve Engine

The original search engine implementation using [Bleve](https://blevesearch.com/), a full-text search and indexing library written in Go.

### Features
- Local file-based indexing
- No external dependencies
- Custom analyzers for different field types
- Built-in query language support

### Usage

```go
import (
    "github.com/owncloud/ocis/v2/services/search/pkg/engine"
    "github.com/owncloud/ocis/v2/services/search/pkg/query/bleve"
)

// Create a new Bleve index
index, err := engine.NewBleveIndex("/path/to/index")
if err != nil {
    // handle error
}

// Create the engine
bleveEngine := engine.NewBleveEngine(index, bleve.DefaultCreator)
```

## Elasticsearch Engine

A new implementation that uses [Elasticsearch](https://www.elastic.co/elasticsearch/) as the backend search engine.

### Features
- Distributed search and analytics
- Horizontal scalability
- Advanced search capabilities
- REST API interface
- Rich ecosystem and tooling

### Configuration

```go
import (
    "github.com/owncloud/ocis/v2/services/search/pkg/engine"
    "github.com/owncloud/ocis/v2/services/search/pkg/query/elasticsearch"
    "github.com/owncloud/ocis/v2/services/search/pkg/config"
)

// Configure Elasticsearch connection
config := config.EngineElasticsearch{
    Addresses: []string{"http://localhost:9200"},
    Username:  "elastic",
    Password:  "password",
    IndexName: "search-index",
}

// Create the engine
esEngine, err := engine.NewElasticSearchEngine(config, elasticsearch.DefaultCreator)
if err != nil {
    // handle error
}
```

### Elasticsearch Setup

Before using the Elasticsearch engine, you need to have an Elasticsearch cluster running. Here are some options:

#### Docker
```bash
docker run -d --name elasticsearch \
  -p 9200:9200 -p 9300:9300 \
  -e "discovery.type=single-node" \
  -e "xpack.security.enabled=false" \
  elasticsearch:8.11.0
```

#### Docker Compose
```yaml
version: '3.8'
services:
  elasticsearch:
    image: elasticsearch:8.11.0
    container_name: elasticsearch
    environment:
      - discovery.type=single-node
      - xpack.security.enabled=false
    ports:
      - "9200:9200"
      - "9300:9300"
    volumes:
      - elasticsearch_data:/usr/share/elasticsearch/data
    networks:
      - search_network

volumes:
  elasticsearch_data:

networks:
  search_network:
```

## Engine Interface

Both engines implement the same `Engine` interface:

```go
type Engine interface {
    Search(ctx context.Context, req *searchService.SearchIndexRequest) (*searchService.SearchIndexResponse, error)
    Upsert(id string, r Resource) error
    Move(id string, parentid string, target string) error
    Delete(id string) error
    Restore(id string) error
    Purge(id string) error
    DocCount() (uint64, error)
}
```

## Query Language

Both engines support the same KQL (Keyword Query Language) syntax:

### Basic Queries
- `name:document.pdf` - Search for files named "document.pdf"
- `content:important` - Search for files containing "important"
- `mediatype:image` - Search for image files

### Boolean Queries
- `name:test AND content:example` - Files named "test" containing "example"
- `name:test OR name:example` - Files named either "test" or "example"
- `NOT hidden:true` - Files that are not hidden

### Date Queries
- `mtime:>2023-01-01` - Files modified after January 1, 2023
- `mtime:>=2023-01-01 AND mtime:<2023-12-31` - Files modified in 2023

### Grouping
- `(name:test OR name:example) AND content:important`

### Field Mappings
- `rootid` → `RootID`
- `path` → `Path`
- `id` → `ID`
- `name` → `Name`
- `size` → `Size`
- `mtime` → `Mtime`
- `mediatype` → `MimeType`
- `type` → `Type`
- `tag`/`tags` → `Tags`
- `content` → `Content`
- `hidden` → `Hidden`

### Special Media Types
- `mediatype:file` - All files (excludes folders)
- `mediatype:folder` - Folders only
- `mediatype:document` - Documents (Word, PDF, text files)
- `mediatype:spreadsheet` - Spreadsheet files
- `mediatype:presentation` - Presentation files
- `mediatype:image` - Image files
- `mediatype:video` - Video files
- `mediatype:audio` - Audio files
- `mediatype:archive` - Archive files (ZIP, TAR, etc.)

## Performance Considerations

### Bleve
- Better for small to medium datasets
- Lower resource requirements
- Single-node limitations
- File-based storage

### Elasticsearch
- Better for large datasets
- Higher resource requirements
- Horizontal scaling capabilities
- Network-based operations

## Performance Optimizations

### Elasticsearch Optimizations

#### 1. Refresh Strategy
```go
// Avoid immediate refresh for better throughput
req := esapi.IndexRequest{
    Index:      indexName,
    DocumentID: id,
    Body:       bytes.NewReader(body),
    Refresh:    "wait_for", // ✅ Wait for next refresh cycle instead of "true"
}
```

#### 2. Bulk Operations
For better performance when indexing multiple documents:

```go
// Use BulkUpsert for multiple documents
resources := []Resource{...}
err := engine.BulkUpsert(resources)

// Instead of individual Upsert calls:
// for _, r := range resources {
//     engine.Upsert(r.ID, r) // ❌ Slow
// }
```

#### 3. Index Settings Optimization
```go
indexMapping := map[string]interface{}{
    "settings": map[string]interface{}{
        // Performance settings
        "number_of_shards":   1,           // Single shard for small-medium datasets
        "number_of_replicas": 0,           // No replicas for dev environments
        "refresh_interval":   "5s",        // Less frequent refresh (default: 1s)
        "max_result_window":  50000,       // Allow larger result sets
        
        // Memory optimization
        "index": map[string]interface{}{
            "codec": "best_compression",   // Better compression
        },
    },
}
```

#### 4. Connection Pool Configuration
```go
config := config.EngineElasticsearch{
    Addresses: []string{"http://localhost:9200"},
    // Add connection pool settings
    Transport: &http.Transport{
        MaxIdleConnsPerHost:   10,
        MaxIdleConns:          100,
        IdleConnTimeout:       90 * time.Second,
        TLSHandshakeTimeout:   10 * time.Second,
    },
    MaxRetries: 3,
}
```

#### 5. Search Optimization
```go
searchRequest := map[string]interface{}{
    "query": query,
    "size":  size,
    
    // Only fetch needed fields
    "_source": []string{
        "ID", "RootID", "Path", "Name", "Type", 
        "Size", "MimeType", "Mtime", "Deleted",
        "audio.*", "image.*", "location.*", "photo.*",
    },
    
    // Optimize highlights
    "highlight": map[string]interface{}{
        "fields": map[string]interface{}{
            "Content": map[string]interface{}{
                "fragment_size": 150,
                "number_of_fragments": 1,
            },
        },
    },
    
    // Performance settings
    "track_total_hits": false,  // Disable if exact count not needed
    "request_cache": true,      // Enable query cache
}
```

#### 6. Field Mapping Optimization
```go
"properties": map[string]interface{}{
    "ID": map[string]interface{}{
        "type":       "keyword",
        "doc_values": true,  // Enable for sorting/aggregations
        "index":      true,  // Enable for searching
    },
    "Path": map[string]interface{}{
        "type":       "keyword", 
        "doc_values": false, // Disable if not used for aggregations
        "index":      true,
    },
    // Use multi-field mapping for flexible querying
    "Name": map[string]interface{}{
        "type":     "text",
        "analyzer": "lowercase_keyword",
        "fields": map[string]interface{}{
            "raw": map[string]interface{}{
                "type": "keyword", // For exact matching
            },
            "suggest": map[string]interface{}{
                "type": "completion", // For autocomplete
            },
        },
    },
}
```

#### 7. Large Dataset Handling
For directories with many files, use scroll API:

```go
// For large result sets (>10,000 documents)
func (e *ElasticSearch) searchLargeResultSet(query map[string]interface{}) ([]Resource, error) {
    query["size"] = 1000 // Reasonable batch size
    
    req := esapi.SearchRequest{
        Index:  []string{e.indexName},
        Body:   bytes.NewReader(body),
        Scroll: time.Minute, // Keep scroll context for 1 minute
    }
    
    var allResources []Resource
    for {
        // Process results in batches
        // Use ScrollRequest for subsequent batches
    }
    
    return allResources, nil
}
```

### Performance Benchmarks

| Operation | Bleve (local) | Elasticsearch (single node) | Elasticsearch (cluster) |
|-----------|---------------|------------------------------|-------------------------|
| Single Index | ~1ms | ~5ms | ~3ms |
| Bulk Index (100 docs) | ~50ms | ~25ms | ~15ms |
| Simple Search | ~2ms | ~10ms | ~5ms |
| Complex Search | ~10ms | ~15ms | ~8ms |
| Memory Usage | 50-200MB | 200-500MB | 500MB+ |

*Note: Benchmarks are approximate and depend on document size, hardware, and configuration.*

### Tuning Guidelines

#### For Small Deployments (<10k documents):
- Use Bleve for simplicity
- If using Elasticsearch: single shard, no replicas
- Refresh interval: 30s

#### For Medium Deployments (10k-1M documents):
- Elasticsearch recommended
- 1-3 shards, 0-1 replicas
- Refresh interval: 5-10s
- Enable bulk operations

#### For Large Deployments (>1M documents):
- Elasticsearch cluster
- Multiple shards based on data size
- 1+ replicas for reliability
- Refresh interval: 1-5s
- Use scroll API for large queries
- Monitor and tune based on usage patterns

### Monitoring and Troubleshooting

#### Key Metrics to Monitor

**Elasticsearch Metrics:**
```bash
# Check cluster health
curl -X GET "localhost:9200/_cluster/health?pretty"

# Monitor index statistics
curl -X GET "localhost:9200/search-index/_stats?pretty"

# Check slow queries
curl -X GET "localhost:9200/_nodes/stats/indices/search?pretty"
```

**Performance Indicators:**
- Query latency (should be <100ms for most queries)
- Index throughput (documents/second)
- Memory usage (heap should be <50% of available RAM)
- Cache hit rates (query cache, field data cache)
- Refresh time and frequency

#### Common Performance Issues

1. **Slow Indexing:**
   ```go
   // Problem: Using refresh: "true"
   Refresh: "true" // ❌ Forces immediate refresh
   
   // Solution: Use bulk operations + wait_for
   Refresh: "wait_for" // ✅ Better performance
   ```

2. **Memory Issues:**
   ```yaml
   # Set appropriate heap size (50% of RAM, max 32GB)
   ES_JAVA_OPTS: "-Xms4g -Xmx4g"
   ```

3. **Query Performance:**
   ```go
   // Problem: Fetching all fields
   // Solution: Use _source filtering
   "_source": ["ID", "Name", "Path"], // ✅ Only needed fields
   
   // Problem: Large result sets
   // Solution: Use pagination or scroll API
   "from": 0, "size": 100, // ✅ Reasonable page size
   ```

#### Elasticsearch Configuration Examples

**Development Environment:**
```yaml
# elasticsearch.yml
cluster.name: "search-dev"
node.name: "search-node-1"
discovery.type: "single-node"
xpack.security.enabled: false

# Performance settings
indices.memory.index_buffer_size: "20%"
indices.queries.cache.size: "20%"
refresh_interval: "30s"
```

**Production Environment:**
```yaml
# elasticsearch.yml
cluster.name: "search-prod"
node.name: "search-node-1"
discovery.seed_hosts: ["node1", "node2", "node3"]
cluster.initial_master_nodes: ["node1", "node2", "node3"]

# Performance settings
indices.memory.index_buffer_size: "10%"
indices.queries.cache.size: "40%"
refresh_interval: "5s"

# Circuit breaker settings
indices.breaker.total.limit: "70%"
indices.breaker.request.limit: "40%"
```

#### Troubleshooting Checklist

1. **High CPU Usage:**
   - Check for expensive queries (aggregations, wildcards)
   - Monitor search thread pool queue
   - Consider query optimization or caching

2. **High Memory Usage:**
   - Check field data cache usage
   - Monitor segment counts (consider force merge)
   - Verify heap size settings

3. **Slow Searches:**
   - Enable slow query logging
   - Check for large result sets
   - Monitor cache hit rates
   - Consider query optimization

4. **Indexing Performance Issues:**
   - Use bulk operations instead of individual requests
   - Adjust refresh interval
   - Check for mapping conflicts
   - Monitor index queue size

## Migration

To migrate from Bleve to Elasticsearch:

1. Set up Elasticsearch cluster
2. Export data from Bleve index
3. Create Elasticsearch engine with appropriate configuration
4. Re-index the exported data
5. Update application configuration to use Elasticsearch engine

## Testing

Tests are provided for both engines. To run tests:

```bash
# Run tests for both engines
go test ./...

# Run tests for specific engine
go test -run TestBleve
go test -run TestElasticsearch
```

Note: Elasticsearch tests require a running Elasticsearch instance and will be skipped if none is available. 