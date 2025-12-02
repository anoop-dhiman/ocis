package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	storageProvider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esapi"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/owncloud/reva/v2/pkg/errtypes"
	"github.com/owncloud/reva/v2/pkg/storagespace"
	"github.com/owncloud/reva/v2/pkg/utils"

	libregraph "github.com/owncloud/libre-graph-api-go"

	searchMessage "github.com/owncloud/ocis/v2/protogen/gen/ocis/messages/search/v0"
	searchService "github.com/owncloud/ocis/v2/protogen/gen/ocis/services/search/v0"
	"github.com/owncloud/ocis/v2/services/search/pkg/config"
	"github.com/owncloud/ocis/v2/services/search/pkg/content"
	searchQuery "github.com/owncloud/ocis/v2/services/search/pkg/query"
)

// ElasticSearch represents a search engine which utilizes Elasticsearch to search and store resources.
type ElasticSearch struct {
	client       *elasticsearch.Client
	indexName    string
	queryCreator searchQuery.Creator[map[string]interface{}]
}

// NewElasticSearchEngine creates a new ElasticSearch instance
func NewElasticSearchEngine(config config.EngineElasticsearch, queryCreator searchQuery.Creator[map[string]interface{}]) (*ElasticSearch, error) {
	cfg := elasticsearch.Config{
		Addresses: config.Addresses,
		Username:  config.Username,
		Password:  config.Password,
	}

	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Elasticsearch client: %w", err)
	}

	indexName := config.IndexName
	if indexName == "" {
		indexName = "search-index"
	}

	es := &ElasticSearch{
		client:       client,
		indexName:    indexName,
		queryCreator: queryCreator,
	}

	// Create index if it doesn't exist
	if err := es.createIndexIfNotExists(); err != nil {
		return nil, fmt.Errorf("failed to create index: %w", err)
	}

	return es, nil
}

// createIndexIfNotExists creates the index with proper mappings if it doesn't exist
func (e *ElasticSearch) createIndexIfNotExists() error {
	// Check if index exists
	req := esapi.IndicesExistsRequest{
		Index: []string{e.indexName},
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode == 200 {
		// Index exists
		return nil
	}

	// Create index with mappings
	indexMapping := map[string]interface{}{
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"ID":       map[string]interface{}{"type": "keyword"},
				"RootID":   map[string]interface{}{"type": "keyword"},
				"Path":     map[string]interface{}{"type": "keyword"},
				"ParentID": map[string]interface{}{"type": "keyword"},
				"Type":     map[string]interface{}{"type": "long"},
				"Deleted":  map[string]interface{}{"type": "boolean"},
				"Hidden":   map[string]interface{}{"type": "boolean"},
				"Name": map[string]interface{}{
					"type":     "text",
					"analyzer": "lowercase_keyword",
					"fields": map[string]interface{}{
						"raw": map[string]interface{}{
							"type": "keyword",
						},
					},
				},
				"Title":    map[string]interface{}{"type": "text", "analyzer": "standard"},
				"Size":     map[string]interface{}{"type": "long"},
				"Mtime":    map[string]interface{}{"type": "date", "format": "date_time"},
				"MimeType": map[string]interface{}{"type": "keyword"},
				"Content": map[string]interface{}{
					"type":     "text",
					"analyzer": "fulltext",
				},
				"Tags": map[string]interface{}{
					"type":     "text",
					"analyzer": "lowercase_keyword",
				},
				// Audio metadata
				"audio.bitrate":     map[string]interface{}{"type": "long"},
				"audio.channels":    map[string]interface{}{"type": "long"},
				"audio.format":      map[string]interface{}{"type": "keyword"},
				"audio.duration":    map[string]interface{}{"type": "long"},
				"audio.album":       map[string]interface{}{"type": "text"},
				"audio.albumArtist": map[string]interface{}{"type": "text"},
				"audio.artist":      map[string]interface{}{"type": "text"},
				"audio.title":       map[string]interface{}{"type": "text"},
				"audio.year":        map[string]interface{}{"type": "long"},
				"audio.genre":       map[string]interface{}{"type": "text"},
				"audio.track":       map[string]interface{}{"type": "long"},
				// Image metadata
				"image.width":  map[string]interface{}{"type": "long"},
				"image.height": map[string]interface{}{"type": "long"},
				// Location metadata
				"location.latitude":  map[string]interface{}{"type": "float"},
				"location.longitude": map[string]interface{}{"type": "float"},
				"location.altitude":  map[string]interface{}{"type": "float"},
				// Photo metadata
				"photo.cameraMake":          map[string]interface{}{"type": "keyword"},
				"photo.cameraModel":         map[string]interface{}{"type": "keyword"},
				"photo.takenDateTime":       map[string]interface{}{"type": "date", "format": "date_time"},
				"photo.focalLength":         map[string]interface{}{"type": "float"},
				"photo.fNumber":             map[string]interface{}{"type": "float"},
				"photo.exposureTime":        map[string]interface{}{"type": "float"},
				"photo.iso":                 map[string]interface{}{"type": "long"},
				"photo.orientationAsNumber": map[string]interface{}{"type": "long"},
			},
		},
		"settings": map[string]interface{}{
			"analysis": map[string]interface{}{
				"analyzer": map[string]interface{}{
					"lowercase_keyword": map[string]interface{}{
						"type":      "custom",
						"tokenizer": "keyword",
						"filter":    []string{"lowercase"},
					},
					"fulltext": map[string]interface{}{
						"type":      "custom",
						"tokenizer": "standard",
						"filter":    []string{"lowercase", "porter_stem"},
					},
				},
			},
		},
	}

	body, err := json.Marshal(indexMapping)
	if err != nil {
		return err
	}

	req2 := esapi.IndicesCreateRequest{
		Index: e.indexName,
		Body:  bytes.NewReader(body),
	}

	res2, err := req2.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res2.Body.Close()

	if res2.IsError() {
		return fmt.Errorf("failed to create index: %s", res2.String())
	}

	return nil
}

// Search executes a search request operation within the index.
func (e *ElasticSearch) Search(ctx context.Context, sir *searchService.SearchIndexRequest) (*searchService.SearchIndexResponse, error) {
	createdQuery, err := e.queryCreator.Create(sir.Query)
	if err != nil {
		if searchQuery.IsValidationError(err) {
			return nil, errtypes.BadRequest(err.Error())
		}
		return nil, err
	}

	// Build the main query
	mustQueries := []map[string]interface{}{
		{
			"term": map[string]interface{}{
				"Deleted": false,
			},
		},
	}

	// Add the created query
	if createdQuery != nil {
		mustQueries = append(mustQueries, createdQuery)
	}

	// Add resource reference filter if specified
	if sir.Ref != nil {
		mustQueries = append(mustQueries, map[string]interface{}{
			"term": map[string]interface{}{
				"RootID": storagespace.FormatResourceID(
					&storageProvider.ResourceId{
						StorageId: sir.Ref.GetResourceId().GetStorageId(),
						SpaceId:   sir.Ref.GetResourceId().GetSpaceId(),
						OpaqueId:  sir.Ref.GetResourceId().GetOpaqueId(),
					},
				),
			},
		})
	}

	query := map[string]interface{}{
		"bool": map[string]interface{}{
			"must": mustQueries,
		},
	}

	// Determine page size
	size := 200
	switch {
	case sir.PageSize == -1:
		size = 10000 // Elasticsearch default max
	case sir.PageSize > 0:
		size = int(sir.PageSize)
	}

	searchRequest := map[string]interface{}{
		"query": query,
		"size":  size,
		"highlight": map[string]interface{}{
			"fields": map[string]interface{}{
				"Content": map[string]interface{}{},
			},
		},
	}

	body, err := json.Marshal(searchRequest)
	if err != nil {
		return nil, err
	}

	req := esapi.SearchRequest{
		Index: []string{e.indexName},
		Body:  bytes.NewReader(body),
	}

	res, err := req.Do(ctx, e.client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search failed: %s", res.String())
	}

	var searchResponse map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&searchResponse); err != nil {
		return nil, err
	}

	return e.parseSearchResponse(searchResponse, sir.Ref)
}

// parseSearchResponse converts Elasticsearch response to SearchIndexResponse
func (e *ElasticSearch) parseSearchResponse(response map[string]interface{}, ref *searchMessage.Reference) (*searchService.SearchIndexResponse, error) {
	hits, ok := response["hits"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid search response format")
	}

	total, _ := hits["total"].(map[string]interface{})["value"].(float64)
	totalMatches := int32(total)

	hitsList, ok := hits["hits"].([]interface{})
	if !ok {
		return nil, errors.New("invalid hits format")
	}

	matches := make([]*searchMessage.Match, 0, len(hitsList))
	actualMatches := int32(0)

	for _, hit := range hitsList {
		hitMap, ok := hit.(map[string]interface{})
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}

		// Apply path filtering if reference is specified
		if ref != nil {
			hitPath := strings.TrimSuffix(getStringValue(source, "Path"), "/")
			requestedPath := utils.MakeRelativePath(ref.Path)
			isRoot := hitPath == requestedPath

			if !isRoot && requestedPath != "." && !strings.HasPrefix(hitPath, requestedPath+"/") {
				totalMatches--
				continue
			}
		}

		match, err := e.buildMatchFromSource(source, hitMap)
		if err != nil {
			continue
		}

		matches = append(matches, match)
		actualMatches++
	}

	return &searchService.SearchIndexResponse{
		Matches:      matches,
		TotalMatches: actualMatches,
	}, nil
}

// buildMatchFromSource creates a Match from Elasticsearch source
func (e *ElasticSearch) buildMatchFromSource(source map[string]interface{}, hitMap map[string]interface{}) (*searchMessage.Match, error) {
	rootID, err := storagespace.ParseID(getStringValue(source, "RootID"))
	if err != nil {
		return nil, err
	}

	rID, err := storagespace.ParseID(getStringValue(source, "ID"))
	if err != nil {
		return nil, err
	}

	pID, _ := storagespace.ParseID(getStringValue(source, "ParentID"))

	score, _ := hitMap["_score"].(float64)

	match := &searchMessage.Match{
		Score: float32(score),
		Entity: &searchMessage.Entity{
			Ref: &searchMessage.Reference{
				ResourceId: resourceIDtoSearchID(&rootID),
				Path:       getStringValue(source, "Path"),
			},
			Id:         resourceIDtoSearchID(&rID),
			Name:       getStringValue(source, "Name"),
			ParentId:   resourceIDtoSearchID(&pID),
			Size:       uint64(getFloatValue(source, "Size")),
			Type:       uint64(getFloatValue(source, "Type")),
			MimeType:   getStringValue(source, "MimeType"),
			Deleted:    getBoolValue(source, "Deleted"),
			Tags:       getStringSliceValue(source, "Tags"),
			Highlights: getHighlightValue(hitMap, "Content"),
			Audio:      getAudioValueFromSource(source),
			Image:      getImageValueFromSource(source),
			Location:   getLocationValueFromSource(source),
			Photo:      getPhotoValueFromSource(source),
		},
	}

	if mtime, err := time.Parse(time.RFC3339, getStringValue(source, "Mtime")); err == nil {
		match.Entity.LastModifiedTime = &timestamppb.Timestamp{
			Seconds: mtime.Unix(),
			Nanos:   int32(mtime.Nanosecond()),
		}
	}

	return match, nil
}

// Upsert indexes or stores Resource data fields.
func (e *ElasticSearch) Upsert(id string, r Resource) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}

	req := esapi.IndexRequest{
		Index:      e.indexName,
		DocumentID: id,
		Body:       bytes.NewReader(body),
		Refresh:    "wait_for",
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("failed to index document: %s", res.String())
	}

	return nil
}

// BulkUpsert indexes multiple resources in bulk.
func (e *ElasticSearch) BulkUpsert(resources []Resource) error {
	if len(resources) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, r := range resources {
		// Index action
		action := map[string]interface{}{
			"index": map[string]interface{}{
				"_index": e.indexName,
				"_id":    r.ID,
			},
		}
		if err := json.NewEncoder(&buf).Encode(action); err != nil {
			return err
		}

		// Document
		if err := json.NewEncoder(&buf).Encode(r); err != nil {
			return err
		}
	}

	req := esapi.BulkRequest{
		Body:    &buf,
		Refresh: "wait_for",
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() {
		return fmt.Errorf("bulk operation failed: %s", res.String())
	}

	return nil
}

// Move updates the resource location and all of its necessary fields.
func (e *ElasticSearch) Move(id string, parentid string, target string) error {
	r, err := e.getResource(id)
	if err != nil {
		return err
	}

	currentPath := r.Path
	nextPath := utils.MakeRelativePath(target)

	r.Path = nextPath
	r.Name = path.Base(nextPath)
	r.ParentID = parentid

	if err := e.Upsert(id, *r); err != nil {
		return err
	}

	// If it's a container, update all children
	if r.Type == uint64(storageProvider.ResourceType_RESOURCE_TYPE_CONTAINER) {
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []map[string]interface{}{
						{
							"term": map[string]interface{}{
								"RootID": r.RootID,
							},
						},
						{
							"wildcard": map[string]interface{}{
								"Path": currentPath + "/*",
							},
						},
					},
				},
			},
			"size": 10000,
		}

		children, err := e.searchResources(query)
		if err != nil {
			return err
		}

		// Bulk update instead of individual updates
		for i := range children {
			children[i].Path = strings.Replace(children[i].Path, currentPath, nextPath, 1)
		}

		return e.BulkUpsert(children)
	}

	return nil
}

// Delete marks the resource as deleted.
func (e *ElasticSearch) Delete(id string) error {
	return e.setDeleted(id, true)
}

// Restore is the counterpart to Delete.
func (e *ElasticSearch) Restore(id string) error {
	return e.setDeleted(id, false)
}

// setDeleted sets the deleted flag for a resource and its children
func (e *ElasticSearch) setDeleted(id string, deleted bool) error {
	r, err := e.getResource(id)
	if err != nil {
		return err
	}

	r.Deleted = deleted
	if err := e.Upsert(id, *r); err != nil {
		return err
	}

	// If it's a container, update all children
	if r.Type == uint64(storageProvider.ResourceType_RESOURCE_TYPE_CONTAINER) {
		query := map[string]interface{}{
			"query": map[string]interface{}{
				"bool": map[string]interface{}{
					"must": []map[string]interface{}{
						{
							"term": map[string]interface{}{
								"RootID": r.RootID,
							},
						},
						{
							"wildcard": map[string]interface{}{
								"Path": r.Path + "/*",
							},
						},
					},
				},
			},
			"size": 10000,
		}

		children, err := e.searchResources(query)
		if err != nil {
			return err
		}

		for _, child := range children {
			child.Deleted = deleted
			if err := e.Upsert(child.ID, child); err != nil {
				return err
			}
		}
	}

	return nil
}

// Purge removes a resource from the index, irreversible operation.
func (e *ElasticSearch) Purge(id string) error {
	req := esapi.DeleteRequest{
		Index:      e.indexName,
		DocumentID: id,
		Refresh:    "true",
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.IsError() && res.StatusCode != 404 {
		return fmt.Errorf("failed to delete document: %s", res.String())
	}

	return nil
}

// DocCount returns the number of resources in the index.
func (e *ElasticSearch) DocCount() (uint64, error) {
	req := esapi.CountRequest{
		Index: []string{e.indexName},
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return 0, fmt.Errorf("failed to count documents: %s", res.String())
	}

	var response map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return 0, err
	}

	count, _ := response["count"].(float64)
	return uint64(count), nil
}

// getResource retrieves a resource by ID
func (e *ElasticSearch) getResource(id string) (*Resource, error) {
	req := esapi.GetRequest{
		Index:      e.indexName,
		DocumentID: id,
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		if res.StatusCode == 404 {
			return nil, errors.New("entity not found")
		}
		return nil, fmt.Errorf("failed to get document: %s", res.String())
	}

	var response map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return nil, err
	}

	source, ok := response["_source"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid document format")
	}

	return e.sourceToResource(source)
}

// searchResources performs a search and returns Resource objects
func (e *ElasticSearch) searchResources(query map[string]interface{}) ([]Resource, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}

	req := esapi.SearchRequest{
		Index: []string{e.indexName},
		Body:  bytes.NewReader(body),
	}

	res, err := req.Do(context.Background(), e.client)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("search failed: %s", res.String())
	}

	var searchResponse map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&searchResponse); err != nil {
		return nil, err
	}

	hits, ok := searchResponse["hits"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid search response format")
	}

	hitsList, ok := hits["hits"].([]interface{})
	if !ok {
		return nil, errors.New("invalid hits format")
	}

	resources := make([]Resource, 0, len(hitsList))
	for _, hit := range hitsList {
		hitMap, ok := hit.(map[string]interface{})
		if !ok {
			continue
		}

		source, ok := hitMap["_source"].(map[string]interface{})
		if !ok {
			continue
		}

		resource, err := e.sourceToResource(source)
		if err != nil {
			continue
		}

		resources = append(resources, *resource)
	}

	return resources, nil
}

// sourceToResource converts Elasticsearch source to Resource
func (e *ElasticSearch) sourceToResource(source map[string]interface{}) (*Resource, error) {
	resource := &Resource{
		ID:       getStringValue(source, "ID"),
		RootID:   getStringValue(source, "RootID"),
		Path:     getStringValue(source, "Path"),
		ParentID: getStringValue(source, "ParentID"),
		Type:     uint64(getFloatValue(source, "Type")),
		Deleted:  getBoolValue(source, "Deleted"),
		Hidden:   getBoolValue(source, "Hidden"),
		Document: content.Document{
			Name:     getStringValue(source, "Name"),
			Title:    getStringValue(source, "Title"),
			Size:     uint64(getFloatValue(source, "Size")),
			Mtime:    getStringValue(source, "Mtime"),
			MimeType: getStringValue(source, "MimeType"),
			Content:  getStringValue(source, "Content"),
			Tags:     getStringSliceValue(source, "Tags"),
			Audio:    getAudioValueFromSourceLibreGraph(source),
			Image:    getImageValueFromSourceLibreGraph(source),
			Location: getLocationValueFromSourceLibreGraph(source),
			Photo:    getPhotoValueFromSourceLibreGraph(source),
		},
	}

	return resource, nil
}

// Helper functions for extracting values from Elasticsearch source

func getStringValue(source map[string]interface{}, key string) string {
	if val, ok := source[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getFloatValue(source map[string]interface{}, key string) float64 {
	if val, ok := source[key]; ok {
		if f, ok := val.(float64); ok {
			return f
		}
	}
	return 0
}

func getBoolValue(source map[string]interface{}, key string) bool {
	if val, ok := source[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func getStringSliceValue(source map[string]interface{}, key string) []string {
	if val, ok := source[key]; ok {
		if slice, ok := val.([]interface{}); ok {
			result := make([]string, len(slice))
			for i, v := range slice {
				if str, ok := v.(string); ok {
					result[i] = str
				}
			}
			return result
		}
		// Handle single string value
		if str, ok := val.(string); ok {
			return []string{str}
		}
	}
	return nil
}

func getHighlightValue(hitMap map[string]interface{}, field string) string {
	if highlight, ok := hitMap["highlight"].(map[string]interface{}); ok {
		if fieldHighlight, ok := highlight[field].([]interface{}); ok && len(fieldHighlight) > 0 {
			if str, ok := fieldHighlight[0].(string); ok {
				return str
			}
		}
	}
	return ""
}

func getAudioValueFromSource(source map[string]interface{}) *searchMessage.Audio {
	mimeType := getStringValue(source, "MimeType")
	if !strings.HasPrefix(mimeType, "audio/") {
		return nil
	}

	audio := &searchMessage.Audio{}
	hasValue := false

	if val := getFloatValue(source, "audio.bitrate"); val != 0 {
		bitrate := int64(val)
		audio.Bitrate = &bitrate
		hasValue = true
	}
	if val := getFloatValue(source, "audio.duration"); val != 0 {
		duration := int64(val)
		audio.Duration = &duration
		hasValue = true
	}
	if val := getStringValue(source, "audio.album"); val != "" {
		audio.Album = &val
		hasValue = true
	}
	if val := getStringValue(source, "audio.albumArtist"); val != "" {
		audio.AlbumArtist = &val
		hasValue = true
	}
	if val := getStringValue(source, "audio.artist"); val != "" {
		audio.Artist = &val
		hasValue = true
	}
	if val := getStringValue(source, "audio.title"); val != "" {
		audio.Title = &val
		hasValue = true
	}
	if val := getFloatValue(source, "audio.year"); val != 0 {
		year := int32(val)
		audio.Year = &year
		hasValue = true
	}
	if val := getStringValue(source, "audio.genre"); val != "" {
		audio.Genre = &val
		hasValue = true
	}
	if val := getFloatValue(source, "audio.track"); val != 0 {
		track := int32(val)
		audio.Track = &track
		hasValue = true
	}

	if hasValue {
		return audio
	}
	return nil
}

func getImageValueFromSource(source map[string]interface{}) *searchMessage.Image {
	image := &searchMessage.Image{}
	hasValue := false

	if val := getFloatValue(source, "image.width"); val != 0 {
		width := int32(val)
		image.Width = &width
		hasValue = true
	}
	if val := getFloatValue(source, "image.height"); val != 0 {
		height := int32(val)
		image.Height = &height
		hasValue = true
	}

	if hasValue {
		return image
	}
	return nil
}

func getLocationValueFromSource(source map[string]interface{}) *searchMessage.GeoCoordinates {
	location := &searchMessage.GeoCoordinates{}
	hasValue := false

	if val := getFloatValue(source, "location.latitude"); val != 0 {
		location.Latitude = &val
		hasValue = true
	}
	if val := getFloatValue(source, "location.longitude"); val != 0 {
		location.Longitude = &val
		hasValue = true
	}
	if val := getFloatValue(source, "location.altitude"); val != 0 {
		location.Altitude = &val
		hasValue = true
	}

	if hasValue {
		return location
	}
	return nil
}

func getPhotoValueFromSource(source map[string]interface{}) *searchMessage.Photo {
	photo := &searchMessage.Photo{}
	hasValue := false

	if val := getStringValue(source, "photo.cameraMake"); val != "" {
		photo.CameraMake = &val
		hasValue = true
	}
	if val := getStringValue(source, "photo.cameraModel"); val != "" {
		photo.CameraModel = &val
		hasValue = true
	}
	if val := getStringValue(source, "photo.takenDateTime"); val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			photo.TakenDateTime = timestamppb.New(t)
			hasValue = true
		}
	}
	if val := getFloatValue(source, "photo.focalLength"); val != 0 {
		focalLength := float32(val)
		photo.FocalLength = &focalLength
		hasValue = true
	}
	if val := getFloatValue(source, "photo.fNumber"); val != 0 {
		fNumber := float32(val)
		photo.FNumber = &fNumber
		hasValue = true
	}
	if val := getFloatValue(source, "photo.iso"); val != 0 {
		iso := int32(val)
		photo.Iso = &iso
		hasValue = true
	}

	if hasValue {
		return photo
	}
	return nil
}

// Helper functions for LibreGraph types (used in Resource.Document)

func getAudioValueFromSourceLibreGraph(source map[string]interface{}) *libregraph.Audio {
	mimeType := getStringValue(source, "MimeType")
	if !strings.HasPrefix(mimeType, "audio/") {
		return nil
	}

	audio := &libregraph.Audio{}
	hasValue := false

	if val := getFloatValue(source, "audio.bitrate"); val != 0 {
		bitrate := int64(val)
		audio.Bitrate = &bitrate
		hasValue = true
	}
	if val := getFloatValue(source, "audio.duration"); val != 0 {
		duration := int64(val)
		audio.Duration = &duration
		hasValue = true
	}
	if val := getStringValue(source, "audio.album"); val != "" {
		audio.Album = &val
		hasValue = true
	}
	if val := getStringValue(source, "audio.albumArtist"); val != "" {
		audio.AlbumArtist = &val
		hasValue = true
	}
	if val := getStringValue(source, "audio.artist"); val != "" {
		audio.Artist = &val
		hasValue = true
	}
	if val := getStringValue(source, "audio.title"); val != "" {
		audio.Title = &val
		hasValue = true
	}
	if val := getFloatValue(source, "audio.year"); val != 0 {
		year := int32(val)
		audio.Year = &year
		hasValue = true
	}
	if val := getStringValue(source, "audio.genre"); val != "" {
		audio.Genre = &val
		hasValue = true
	}
	if val := getFloatValue(source, "audio.track"); val != 0 {
		track := int32(val)
		audio.Track = &track
		hasValue = true
	}

	if hasValue {
		return audio
	}
	return nil
}

func getImageValueFromSourceLibreGraph(source map[string]interface{}) *libregraph.Image {
	image := &libregraph.Image{}
	hasValue := false

	if val := getFloatValue(source, "image.width"); val != 0 {
		width := int32(val)
		image.Width = &width
		hasValue = true
	}
	if val := getFloatValue(source, "image.height"); val != 0 {
		height := int32(val)
		image.Height = &height
		hasValue = true
	}

	if hasValue {
		return image
	}
	return nil
}

func getLocationValueFromSourceLibreGraph(source map[string]interface{}) *libregraph.GeoCoordinates {
	location := &libregraph.GeoCoordinates{}
	hasValue := false

	if val := getFloatValue(source, "location.latitude"); val != 0 {
		location.Latitude = &val
		hasValue = true
	}
	if val := getFloatValue(source, "location.longitude"); val != 0 {
		location.Longitude = &val
		hasValue = true
	}
	if val := getFloatValue(source, "location.altitude"); val != 0 {
		location.Altitude = &val
		hasValue = true
	}

	if hasValue {
		return location
	}
	return nil
}

func getPhotoValueFromSourceLibreGraph(source map[string]interface{}) *libregraph.Photo {
	photo := &libregraph.Photo{}
	hasValue := false

	if val := getStringValue(source, "photo.cameraMake"); val != "" {
		photo.CameraMake = &val
		hasValue = true
	}
	if val := getStringValue(source, "photo.cameraModel"); val != "" {
		photo.CameraModel = &val
		hasValue = true
	}
	if val := getStringValue(source, "photo.takenDateTime"); val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			photo.TakenDateTime = &t
			hasValue = true
		}
	}
	if val := getFloatValue(source, "photo.focalLength"); val != 0 {
		photo.FocalLength = &val
		hasValue = true
	}
	if val := getFloatValue(source, "photo.fNumber"); val != 0 {
		photo.FNumber = &val
		hasValue = true
	}
	if val := getFloatValue(source, "photo.iso"); val != 0 {
		iso := int32(val)
		photo.Iso = &iso
		hasValue = true
	}

	if hasValue {
		return photo
	}
	return nil
}
