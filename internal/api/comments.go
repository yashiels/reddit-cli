package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var GraphQLBase = "https://gql-fed.reddit.com/"

type NativeCommentsPost struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Subreddit struct {
		Name string `json:"name"`
	} `json:"subreddit"`
	CommentCount  int `json:"commentCount"`
	CommentForest *struct {
		Trees []struct {
			Depth int `json:"depth"`
			Node  *struct {
				Type       string `json:"__typename"`
				AuthorInfo *struct {
					Name string `json:"name"`
				} `json:"authorInfo"`
				Score   *int `json:"score"`
				Content *struct {
					Markdown string `json:"markdown"`
				} `json:"content"`
				Permalink string `json:"permalink"`
			} `json:"node"`
		} `json:"trees"`
	} `json:"commentForest"`
}

func (c *Client) CommentsRaw(id, sort string, limit int) ([]byte, *NativeCommentsPost, error) {
	if !c.native {
		data, err := c.GetRaw("/comments/"+id, url.Values{"limit": {fmt.Sprint(limit)}, "sort": {sort}, "raw_json": {"1"}})
		return data, nil, err
	}
	sortTypes := map[string]string{"confidence": "CONFIDENCE", "top": "TOP", "new": "NEW", "controversial": "CONTROVERSIAL", "old": "OLD", "qa": "QA"}
	sortType, ok := sortTypes[sort]
	if !ok || limit < 1 || limit > 100 {
		return nil, nil, fmt.Errorf("invalid native comments parameters")
	}
	id = "t3_" + strings.TrimPrefix(id, "t3_")
	body, err := json.Marshal(map[string]any{
		"operationName": "PostComments",
		"variables":     map[string]any{"id": id, "sortType": sortType, "count": limit, "maxDepth": 1, "includePremiumAvatarTreatment": false, "includeConsistencyToken": false},
		"extensions":    map[string]any{"persistedQuery": map[string]any{"version": 1, "sha256Hash": "314f29c2494fd78c7844ef2c6b09acb4bef5b37ab50b4ddc01691d6d5c8433f8"}},
	})
	if err != nil {
		return nil, nil, err
	}
	token, err := c.token()
	if err != nil {
		return nil, nil, err
	}
	data, err := c.requestBody(http.MethodPost, GraphQLBase, string(body), "application/json", token, false)
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		Errors []json.RawMessage `json:"errors"`
		Data   struct {
			Post *NativeCommentsPost `json:"postInfoById"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &out) != nil || len(out.Errors) != 0 || out.Data.Post == nil || out.Data.Post.ID != id || out.Data.Post.CommentForest == nil || out.Data.Post.CommentForest.Trees == nil {
		return nil, nil, fmt.Errorf("invalid native comments response")
	}
	return data, out.Data.Post, nil
}
