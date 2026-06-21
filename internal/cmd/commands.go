package cmd

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/yashiels/reddit-cli/internal/api"
	"github.com/yashiels/reddit-cli/internal/auth"
)

// ---- listing types (only the fields we render) ----

type listing struct {
	Data struct {
		After    string `json:"after"`
		Children []struct {
			Kind string `json:"kind"`
			Data thing  `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

type thing struct {
	ID          string `json:"id"`
	Name        string `json:"name"` // fullname, e.g. t3_abc123
	Title       string `json:"title"`
	Author      string `json:"author"`
	Subreddit   string `json:"subreddit"`
	Score       int    `json:"score"`
	NumComments int    `json:"num_comments"`
	Permalink   string `json:"permalink"`
	SelfText    string `json:"selftext"`
	Body        string `json:"body"`
}

// ---- login ----

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with a Reddit username and password",
	Long: "Logs in via the official app's password grant and stores the token at\n" +
		"$XDG_CONFIG_HOME/reddit-cli/credentials.json (0600). The password is stored\n" +
		"alongside it so the token can refresh silently — it has full account access,\n" +
		"so treat that file like a password.",
	RunE: func(cmd *cobra.Command, args []string) error {
		clientID, _ := cmd.Flags().GetString("client-id")
		clientSecret, _ := cmd.Flags().GetString("client-secret")
		user, _ := cmd.Flags().GetString("user")
		pass, _ := cmd.Flags().GetString("password")
		otp, _ := cmd.Flags().GetString("otp")
		token, _ := cmd.Flags().GetString("access-token")

		// No-script-app path: store a bearer lifted from a logged-in browser session.
		if token != "" {
			c, err := auth.LoginWithToken(user, token)
			if err != nil {
				return err
			}
			fmt.Printf("stored access token for %s (valid ~%s)\n", c.Username, c.ExpiresAt.Format("15:04:05"))
			return nil
		}

		r := bufio.NewReader(os.Stdin)
		if clientID == "" {
			fmt.Fprint(os.Stderr, "script-app client id: ")
			line, _ := r.ReadString('\n')
			clientID = strings.TrimSpace(line)
		}
		if user == "" {
			fmt.Fprint(os.Stderr, "username: ")
			line, _ := r.ReadString('\n')
			user = strings.TrimSpace(line)
		}
		if pass == "" {
			fmt.Fprint(os.Stderr, "password: ")
			b, err := term.ReadPassword(syscall.Stdin)
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			pass = strings.TrimSpace(string(b))
		}
		if user == "" || pass == "" {
			return fmt.Errorf("username and password are required")
		}

		c, err := auth.Login(clientID, clientSecret, user, pass, otp)
		if err != nil {
			return err
		}
		fmt.Printf("logged in as %s (token valid until %s)\n", c.Username, c.ExpiresAt.Format("15:04:05"))
		return nil
	},
}

// ---- whoami ----

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the logged-in account",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := api.New()
		if err != nil {
			return err
		}
		if err := c.RequireUser(); err != nil {
			return err
		}
		var me struct {
			Name         string `json:"name"`
			TotalKarma   int    `json:"total_karma"`
			LinkKarma    int    `json:"link_karma"`
			CommentKarma int    `json:"comment_karma"`
		}
		if err := c.Get("/api/v1/me", nil, &me); err != nil {
			return err
		}
		if jsonOut {
			return printJSON(me)
		}
		fmt.Printf("u/%s — %d karma (%d link, %d comment)\n", me.Name, me.TotalKarma, me.LinkKarma, me.CommentKarma)
		return nil
	},
}

// ---- feed ----

var feedCmd = &cobra.Command{
	Use:   "feed [subreddit]",
	Short: "List posts from your front page or a subreddit",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sort, _ := cmd.Flags().GetString("sort")
		limit, _ := cmd.Flags().GetInt("limit")

		path := "/" + sort
		if len(args) == 1 {
			path = "/r/" + strings.TrimPrefix(args[0], "r/") + "/" + sort
		}
		c, err := api.New()
		if err != nil {
			return err
		}
		var l listing
		if err := c.Get(path, url.Values{"limit": {fmt.Sprint(limit)}, "raw_json": {"1"}}, &l); err != nil {
			return err
		}
		if jsonOut {
			return printJSON(l)
		}
		for i, ch := range l.Data.Children {
			t := ch.Data
			fmt.Printf("%2d. [%5d] %s\n", i+1, t.Score, t.Title)
			fmt.Printf("    r/%s · u/%s · %d comments · %s\n", t.Subreddit, t.Author, t.NumComments, t.Name)
		}
		return nil
	},
}

// ---- comments ----

var commentsCmd = &cobra.Command{
	Use:   "comments <post-id>",
	Short: "Show a post and its top-level comments",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")
		id := strings.TrimPrefix(args[0], "t3_")

		c, err := api.New()
		if err != nil {
			return err
		}
		// /comments/<id> returns [postListing, commentsListing]
		var out []listing
		if err := c.Get("/comments/"+id, url.Values{"limit": {fmt.Sprint(limit)}, "raw_json": {"1"}}, &out); err != nil {
			return err
		}
		if jsonOut {
			return printJSON(out)
		}
		if len(out) > 0 && len(out[0].Data.Children) > 0 {
			p := out[0].Data.Children[0].Data
			fmt.Printf("%s\n(r/%s · u/%s · %d points)\n", p.Title, p.Subreddit, p.Author, p.Score)
			if p.SelfText != "" {
				fmt.Printf("\n%s\n", p.SelfText)
			}
			fmt.Println(strings.Repeat("-", 40))
		}
		if len(out) > 1 {
			for _, ch := range out[1].Data.Children {
				t := ch.Data
				if t.Body == "" {
					continue
				}
				fmt.Printf("u/%s [%d]: %s\n", t.Author, t.Score, oneLine(t.Body))
			}
		}
		return nil
	},
}

// ---- user (public profile; works anonymously) ----

var userCmd = &cobra.Command{
	Use:   "user <name>",
	Short: "Show a user's public profile",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimPrefix(args[0], "u/")
		c, err := api.New()
		if err != nil {
			return err
		}
		var about struct {
			Data struct {
				Name         string  `json:"name"`
				TotalKarma   int     `json:"total_karma"`
				LinkKarma    int     `json:"link_karma"`
				CommentKarma int     `json:"comment_karma"`
				CreatedUTC   float64 `json:"created_utc"`
			} `json:"data"`
		}
		if err := c.Get("/user/"+name+"/about", url.Values{"raw_json": {"1"}}, &about); err != nil {
			return err
		}
		if jsonOut {
			return printJSON(about)
		}
		d := about.Data
		fmt.Printf("u/%s — %d karma (%d link, %d comment), created %s\n",
			d.Name, d.TotalKarma, d.LinkKarma, d.CommentKarma,
			time.Unix(int64(d.CreatedUTC), 0).Format("2006-01-02"))
		return nil
	},
}

// ---- search (works anonymously) ----

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search posts, optionally within a subreddit",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sub, _ := cmd.Flags().GetString("sub")
		sort, _ := cmd.Flags().GetString("sort")
		limit, _ := cmd.Flags().GetInt("limit")
		q := strings.Join(args, " ")

		path := "/search"
		params := url.Values{"q": {q}, "sort": {sort}, "limit": {fmt.Sprint(limit)}, "raw_json": {"1"}, "type": {"link"}}
		if sub != "" {
			path = "/r/" + strings.TrimPrefix(sub, "r/") + "/search"
			params.Set("restrict_sr", "1")
		}
		c, err := api.New()
		if err != nil {
			return err
		}
		var l listing
		if err := c.Get(path, params, &l); err != nil {
			return err
		}
		if jsonOut {
			return printJSON(l)
		}
		for i, ch := range l.Data.Children {
			t := ch.Data
			fmt.Printf("%2d. [%5d] %s\n    r/%s · u/%s · %d comments · %s\n", i+1, t.Score, t.Title, t.Subreddit, t.Author, t.NumComments, t.Name)
		}
		return nil
	},
}

// ---- subreddit info (works anonymously) ----

var subredditCmd = &cobra.Command{
	Use:   "subreddit <name>",
	Short: "Show a subreddit's info",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimPrefix(args[0], "r/")
		c, err := api.New()
		if err != nil {
			return err
		}
		var about struct {
			Data struct {
				DisplayName       string  `json:"display_name"`
				Title             string  `json:"title"`
				PublicDescription string  `json:"public_description"`
				Subscribers       int     `json:"subscribers"`
				ActiveUsers       int     `json:"active_user_count"`
				CreatedUTC        float64 `json:"created_utc"`
			} `json:"data"`
		}
		if err := c.Get("/r/"+name+"/about", url.Values{"raw_json": {"1"}}, &about); err != nil {
			return err
		}
		if jsonOut {
			return printJSON(about)
		}
		d := about.Data
		fmt.Printf("r/%s — %s\n%d subscribers · %d online · since %s\n\n%s\n",
			d.DisplayName, d.Title, d.Subscribers, d.ActiveUsers,
			time.Unix(int64(d.CreatedUTC), 0).Format("2006-01-02"), d.PublicDescription)
		return nil
	},
}

// ---- posts: a user's submissions or comments (works anonymously) ----

var postsCmd = &cobra.Command{
	Use:   "posts <user>",
	Short: "List a user's recent submissions (or --comments)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		comments, _ := cmd.Flags().GetBool("comments")
		limit, _ := cmd.Flags().GetInt("limit")
		name := strings.TrimPrefix(args[0], "u/")
		kind := "submitted"
		if comments {
			kind = "comments"
		}
		c, err := api.New()
		if err != nil {
			return err
		}
		var l listing
		if err := c.Get("/user/"+name+"/"+kind, url.Values{"limit": {fmt.Sprint(limit)}, "raw_json": {"1"}}, &l); err != nil {
			return err
		}
		if jsonOut {
			return printJSON(l)
		}
		for i, ch := range l.Data.Children {
			t := ch.Data
			if comments {
				fmt.Printf("%2d. [%4d] r/%s: %s\n", i+1, t.Score, t.Subreddit, oneLine(t.Body))
			} else {
				fmt.Printf("%2d. [%5d] %s\n    r/%s · %d comments · %s\n", i+1, t.Score, t.Title, t.Subreddit, t.NumComments, t.Name)
			}
		}
		return nil
	},
}

// ---- vote ----

var voteCmd = &cobra.Command{
	Use:   "vote <fullname> <up|down|none>",
	Short: "Up/down/clear your vote on a post or comment (e.g. t3_abc, t1_xyz)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, ok := map[string]string{"up": "1", "down": "-1", "none": "0"}[args[1]]
		if !ok {
			return fmt.Errorf("direction must be up, down, or none")
		}
		c, err := api.New()
		if err != nil {
			return err
		}
		if err := c.RequireUser(); err != nil {
			return err
		}
		if err := c.Post("/api/vote", url.Values{"id": {args[0]}, "dir": {dir}}, nil); err != nil {
			return err
		}
		fmt.Printf("voted %s on %s\n", args[1], args[0])
		return nil
	},
}

// ---- reply ----

var replyCmd = &cobra.Command{
	Use:   "reply <fullname> <text>",
	Short: "Reply to a post or comment",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := api.New()
		if err != nil {
			return err
		}
		if err := c.RequireUser(); err != nil {
			return err
		}
		form := url.Values{"thing_id": {args[0]}, "text": {args[1]}, "api_type": {"json"}}
		if err := c.Post("/api/comment", form, nil); err != nil {
			return err
		}
		fmt.Printf("replied to %s\n", args[0])
		return nil
	},
}

func oneLine(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func init() {
	loginCmd.Flags().String("client-id", "", "script-app client id (prompted if omitted)")
	loginCmd.Flags().String("client-secret", "", "script-app client secret")
	loginCmd.Flags().String("user", "", "username (prompted if omitted)")
	loginCmd.Flags().String("password", "", "password (prompted securely if omitted)")
	loginCmd.Flags().String("otp", "", "2FA code, if enabled")
	loginCmd.Flags().String("access-token", "", "store a bearer token lifted from a logged-in reddit.com session (no script app)")

	feedCmd.Flags().String("sort", "hot", "hot|new|top|rising|best")
	feedCmd.Flags().Int("limit", 25, "number of posts")
	commentsCmd.Flags().Int("limit", 20, "number of comments")

	searchCmd.Flags().String("sub", "", "restrict to a subreddit")
	searchCmd.Flags().String("sort", "relevance", "relevance|hot|top|new|comments")
	searchCmd.Flags().Int("limit", 25, "number of results")
	postsCmd.Flags().Bool("comments", false, "list comments instead of submissions")
	postsCmd.Flags().Int("limit", 25, "number of items")

	rootCmd.AddCommand(loginCmd, whoamiCmd, userCmd, feedCmd, commentsCmd,
		searchCmd, subredditCmd, postsCmd, voteCmd, replyCmd)
}
