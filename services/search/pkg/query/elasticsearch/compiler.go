package elasticsearch

import (
	"fmt"
	"strings"
	"time"

	"github.com/owncloud/ocis/v2/ocis-pkg/ast"
	"github.com/owncloud/ocis/v2/ocis-pkg/kql"
)

var _fields = map[string]string{
	"rootid":    "RootID",
	"path":      "Path",
	"id":        "ID",
	"name":      "Name",
	"size":      "Size",
	"mtime":     "Mtime",
	"mediatype": "MimeType",
	"type":      "Type",
	"tag":       "Tags",
	"tags":      "Tags",
	"content":   "Content",
	"hidden":    "Hidden",
}

// Compiler represents a KQL query search string to the Elasticsearch query formatter.
type Compiler struct{}

// Compile implements the query formatter which converts the KQL query search string to the Elasticsearch query.
func (c Compiler) Compile(givenAst *ast.Ast) (map[string]interface{}, error) {
	q, err := compile(givenAst)
	if err != nil {
		return nil, err
	}
	return q, nil
}

func compile(a *ast.Ast) (map[string]interface{}, error) {
	q, _, err := walk(0, a.Nodes)
	if err != nil {
		return nil, err
	}

	// Wrap in bool query if not already a complex query
	switch q["bool"] {
	case nil:
		return map[string]interface{}{
			"bool": map[string]interface{}{
				"must": []interface{}{q},
			},
		}, nil
	default:
		return q, nil
	}
}

func walk(offset int, nodes []ast.Node) (map[string]interface{}, int, error) {
	var prev, next map[string]interface{}
	var operator *ast.OperatorNode
	var isGroup bool

	for i := offset; i < len(nodes); i++ {
		switch n := nodes[i].(type) {
		case *ast.StringNode:
			k := getField(n.Key)
			v := n.Value

			if k != "Hidden" {
				v = strings.ToLower(v)
			}

			var q map[string]interface{}
			var group bool
			switch k {
			case "MimeType":
				q, group = mimeType(k, v)
				if prev == nil {
					isGroup = group
				}
			case "Name", "Content", "Tags":
				// Use match query for text fields
				if strings.Contains(v, "*") || strings.Contains(v, "?") {
					// Use wildcard query for pattern matching
					q = map[string]interface{}{
						"wildcard": map[string]interface{}{
							k: map[string]interface{}{
								"value": v,
							},
						},
					}
				} else {
					q = map[string]interface{}{
						"match": map[string]interface{}{
							k: map[string]interface{}{
								"query": v,
							},
						},
					}
				}
			default:
				// Use term query for exact matches
				q = map[string]interface{}{
					"term": map[string]interface{}{
						k: v,
					},
				}
			}

			if prev == nil {
				prev = q
			} else {
				next = q
			}

		case *ast.DateTimeNode:
			field := getField(n.Key)
			if n.Operator == nil {
				continue
			}

			var rangeQuery map[string]interface{}
			switch n.Operator.Value {
			case ">":
				rangeQuery = map[string]interface{}{
					"range": map[string]interface{}{
						field: map[string]interface{}{
							"gt": n.Value.Format(time.RFC3339),
						},
					},
				}
			case ">=":
				rangeQuery = map[string]interface{}{
					"range": map[string]interface{}{
						field: map[string]interface{}{
							"gte": n.Value.Format(time.RFC3339),
						},
					},
				}
			case "<":
				rangeQuery = map[string]interface{}{
					"range": map[string]interface{}{
						field: map[string]interface{}{
							"lt": n.Value.Format(time.RFC3339),
						},
					},
				}
			case "<=":
				rangeQuery = map[string]interface{}{
					"range": map[string]interface{}{
						field: map[string]interface{}{
							"lte": n.Value.Format(time.RFC3339),
						},
					},
				}
			default:
				continue
			}

			if prev == nil {
				prev = rangeQuery
			} else {
				next = rangeQuery
			}

		case *ast.BooleanNode:
			q := map[string]interface{}{
				"term": map[string]interface{}{
					getField(n.Key): n.Value,
				},
			}
			if prev == nil {
				prev = q
			} else {
				next = q
			}

		case *ast.GroupNode:
			if n.Key != "" {
				n = normalizeGroupingProperty(n)
			}
			q, _, err := walk(0, n.Nodes)
			if err != nil {
				return nil, 0, err
			}
			if prev == nil {
				prev = q
				isGroup = true
			} else {
				next = q
			}

		case *ast.OperatorNode:
			switch n.Value {
			case kql.BoolAND, kql.BoolOR:
				operator = n
			case kql.BoolNOT:
				var err error
				next, offset, err = nextNode(i+1, nodes)
				if err != nil {
					return nil, 0, err
				}
				q := map[string]interface{}{
					"bool": map[string]interface{}{
						"must_not": []interface{}{next},
					},
				}
				if prev == nil {
					// unary in the beginning
					prev = q
				} else {
					next = q
				}
			}
		}

		if prev != nil && next != nil && operator != nil {
			prev = mapBinary(operator, prev, next, isGroup)
			isGroup = false
			operator = nil
			next = nil
		}
		if i < offset {
			i = offset
		}
	}

	if prev == nil {
		return nil, 0, fmt.Errorf("can not compile the query")
	}
	return prev, offset, nil
}

func nextNode(offset int, nodes []ast.Node) (map[string]interface{}, int, error) {
	if n, ok := nodes[offset].(*ast.GroupNode); ok {
		gq, _, err := walk(0, n.Nodes)
		if err != nil {
			return nil, 0, err
		}
		return gq, offset + 1, nil
	}
	if n, ok := nodes[offset].(*ast.OperatorNode); ok {
		if n.Value == kql.BoolNOT {
			return walk(offset, nodes)
		}
	}
	one := nodes[:offset+1]
	return walk(offset, one)
}

func mapBinary(operator *ast.OperatorNode, ln, rn map[string]interface{}, leftIsGroup bool) map[string]interface{} {
	if operator.Value == kql.BoolOR {
		// Handle OR operations
		leftBool, leftIsBool := ln["bool"].(map[string]interface{})
		rightBool, rightIsBool := rn["bool"].(map[string]interface{})

		if leftIsBool && leftBool["should"] != nil {
			// Left is already a should query, add right to it
			should := leftBool["should"].([]interface{})
			if rightIsBool && rightBool["should"] != nil {
				// Right is also a should query, merge them
				rightShould := rightBool["should"].([]interface{})
				should = append(should, rightShould...)
			} else {
				should = append(should, rn)
			}
			leftBool["should"] = should
			return ln
		} else {
			// Create new should query
			should := []interface{}{ln}
			if rightIsBool && rightBool["should"] != nil {
				rightShould := rightBool["should"].([]interface{})
				should = append(should, rightShould...)
			} else {
				should = append(should, rn)
			}
			return map[string]interface{}{
				"bool": map[string]interface{}{
					"should": should,
				},
			}
		}
	}

	if operator.Value == kql.BoolAND {
		// Handle AND operations
		leftBool, leftIsBool := ln["bool"].(map[string]interface{})
		rightBool, rightIsBool := rn["bool"].(map[string]interface{})

		if leftIsBool && leftBool["must"] != nil {
			// Left is already a must query, add right to it
			must := leftBool["must"].([]interface{})
			if rightIsBool && rightBool["must"] != nil {
				// Right is also a must query, merge them
				rightMust := rightBool["must"].([]interface{})
				must = append(must, rightMust...)
			} else {
				must = append(must, rn)
			}
			leftBool["must"] = must
			return ln
		} else if leftIsBool && leftBool["should"] != nil && !leftIsGroup {
			// Left is a should query, need to handle precedence
			should := leftBool["should"].([]interface{})
			if len(should) > 0 {
				// Take the last item from should and create a must with it and rn
				lastItem := should[len(should)-1]
				newMust := map[string]interface{}{
					"bool": map[string]interface{}{
						"must": []interface{}{lastItem, rn},
					},
				}

				if len(should) == 1 {
					// Only one item in should, replace with must
					return newMust
				} else {
					// Multiple items in should, replace last with must
					should[len(should)-1] = newMust
					leftBool["should"] = should
					return ln
				}
			}
		}

		// Create new must query
		must := []interface{}{ln}
		if rightIsBool && rightBool["must"] != nil {
			rightMust := rightBool["must"].([]interface{})
			must = append(must, rightMust...)
		} else {
			must = append(must, rn)
		}
		return map[string]interface{}{
			"bool": map[string]interface{}{
				"must": must,
			},
		}
	}

	// Default to AND
	return map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []interface{}{ln, rn},
		},
	}
}

func getField(name string) string {
	if name == "" {
		return "Name"
	}
	if _, ok := _fields[strings.ToLower(name)]; ok {
		return _fields[strings.ToLower(name)]
	}
	return name
}

func normalizeGroupingProperty(group *ast.GroupNode) *ast.GroupNode {
	for _, n := range group.Nodes {
		if onode, ok := n.(*ast.StringNode); ok {
			onode.Key = group.Key
		}
	}
	return group
}

func mimeType(k, v string) (map[string]interface{}, bool) {
	switch v {
	case "file":
		return map[string]interface{}{
			"bool": map[string]interface{}{
				"must_not": []interface{}{
					map[string]interface{}{
						"term": map[string]interface{}{
							k: "httpd/unix-directory",
						},
					},
				},
			},
		}, false
	case "folder":
		return map[string]interface{}{
			"term": map[string]interface{}{
				k: "httpd/unix-directory",
			},
		}, false
	case "document":
		return map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{k: "application/msword"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.openxmlformats-officedocument.wordprocessingml.form"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.oasis.opendocument.text"}},
					map[string]interface{}{"term": map[string]interface{}{k: "text/plain"}},
					map[string]interface{}{"term": map[string]interface{}{k: "text/markdown"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/rtf"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.apple.pages"}},
				},
			},
		}, true
	case "spreadsheet":
		return map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.ms-excel"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.oasis.opendocument.spreadsheet"}},
					map[string]interface{}{"term": map[string]interface{}{k: "text/csv"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.oasis.opendocument.spreadsheet"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.apple.numbers"}},
				},
			},
		}, true
	case "presentation":
		return map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.openxmlformats-officedocument.presentationml.presentation"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.oasis.opendocument.presentation"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.ms-powerpoint"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/vnd.apple.keynote"}},
				},
			},
		}, true
	case "pdf":
		return map[string]interface{}{
			"term": map[string]interface{}{
				k: "application/pdf",
			},
		}, false
	case "image":
		return map[string]interface{}{
			"wildcard": map[string]interface{}{
				k: map[string]interface{}{
					"value": "image/*",
				},
			},
		}, false
	case "video":
		return map[string]interface{}{
			"wildcard": map[string]interface{}{
				k: map[string]interface{}{
					"value": "video/*",
				},
			},
		}, false
	case "audio":
		return map[string]interface{}{
			"wildcard": map[string]interface{}{
				k: map[string]interface{}{
					"value": "audio/*",
				},
			},
		}, false
	case "archive":
		return map[string]interface{}{
			"bool": map[string]interface{}{
				"should": []interface{}{
					map[string]interface{}{"term": map[string]interface{}{k: "application/zip"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/gzip"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/x-gzip"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/x-7z-compressed"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/x-rar-compressed"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/x-tar"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/x-bzip2"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/x-bzip"}},
					map[string]interface{}{"term": map[string]interface{}{k: "application/x-tgz"}},
				},
			},
		}, true
	default:
		return map[string]interface{}{
			"term": map[string]interface{}{
				k: v,
			},
		}, false
	}
}
