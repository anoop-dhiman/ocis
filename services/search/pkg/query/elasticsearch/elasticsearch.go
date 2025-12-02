// Package elasticsearch provides the ability to work with Elasticsearch queries.
package elasticsearch

import (
	"github.com/owncloud/ocis/v2/ocis-pkg/kql"
	"github.com/owncloud/ocis/v2/services/search/pkg/query"
)

// Creator combines a Builder and a Compiler which is used to Create the query.
type Creator[T any] struct {
	builder  query.Builder
	compiler query.Compiler[T]
}

// Create implements the Creator interface
func (c Creator[T]) Create(qs string) (T, error) {
	var t T
	builderAst, err := c.builder.Build(qs)
	if err != nil {
		return t, err
	}

	t, err = c.compiler.Compile(builderAst)
	if err != nil {
		return t, err
	}

	return t, nil
}

// DefaultCreator exposes a kql to Elasticsearch query creator.
var DefaultCreator = Creator[map[string]interface{}]{kql.Builder{}, Compiler{}}
