package cmd

import (
	"fmt"
	"strings"

	"github.com/yashiels/reddit-cli/internal/api"
)

func printNativeComments(post *api.NativeCommentsPost, limit int) {
	fmt.Printf("%s\n(r/%s · %d comments)\n", post.Title, post.Subreddit.Name, post.CommentCount)
	fmt.Println(strings.Repeat("-", 40))
	shown := 0
	for _, tree := range post.CommentForest.Trees {
		if tree.Depth != 0 || tree.Node == nil {
			continue
		}
		if shown >= limit {
			break
		}
		node := tree.Node
		author := "[unknown]"
		if node.AuthorInfo != nil && node.AuthorInfo.Name != "" {
			author = "u/" + node.AuthorInfo.Name
		}
		body := "[unavailable]"
		if node.Type == "DeletedComment" {
			if node.AuthorInfo == nil || node.AuthorInfo.Name == "" {
				author = "[deleted]"
			}
			body = "[deleted]"
		} else if node.Content != nil && node.Content.Markdown != "" {
			body = oneLine(node.Content.Markdown)
		}
		score := ""
		if node.Score != nil {
			score = fmt.Sprintf(" [%d]", *node.Score)
		}
		fmt.Printf("%s%s: %s\n", author, score, body)
		if link := permalinkURL(node.Permalink); link != "" {
			fmt.Printf("    %s\n", link)
		}
		shown++
	}
}
