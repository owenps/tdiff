package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Runner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

type Client struct {
	Dir    string
	Runner Runner
}

func NewClient(dir string) Client {
	return Client{Dir: dir, Runner: ExecRunner{}}
}

type AttachedPR struct {
	Owner       string   `json:"owner"`
	Repo        string   `json:"repo"`
	Number      int      `json:"pr_number"`
	URL         string   `json:"url,omitempty"`
	Title       string   `json:"title,omitempty"`
	AuthorLogin string   `json:"author_login,omitempty"`
	HeadRef     string   `json:"head_ref,omitempty"`
	BaseRef     string   `json:"base_ref,omitempty"`
	HeadOID     string   `json:"head_oid,omitempty"`
	Status      PRStatus `json:"status,omitempty"`
}

type PullRequest struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	AuthorLogin string    `json:"author_login"`
	HeadRef     string    `json:"head_ref"`
	BaseRef     string    `json:"base_ref"`
	HeadOID     string    `json:"head_oid"`
	IsDraft     bool      `json:"is_draft"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Thread struct {
	ID        string
	Path      string
	Line      int
	StartLine int
	Side      string
	StartSide string
	Resolved  bool
	Outdated  bool
	Comments  []Comment
}

type Comment struct {
	ID        string
	Body      string
	URL       string
	Author    Author
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Author struct {
	Login     string
	Name      string
	AvatarURL string
}

func (c Client) AutoDetectPR(ctx context.Context) (AttachedPR, error) {
	pr, err := c.PRView(ctx)
	if err != nil {
		return AttachedPR{}, err
	}
	return pr, nil
}

const prViewFields = "number,url,title,author,headRefName,baseRefName,headRefOid,isDraft,state,mergedAt,mergeable,reviewDecision,statusCheckRollup"

func (c Client) PRView(ctx context.Context, number ...int) (AttachedPR, error) {
	args := []string{"pr", "view", "--json", prViewFields}
	if len(number) > 0 && number[0] > 0 {
		args = append([]string{"pr", "view", strconv.Itoa(number[0])}, args[2:]...)
	}
	b, err := c.run(ctx, args...)
	if err != nil {
		return AttachedPR{}, err
	}
	var raw struct {
		Number            int             `json:"number"`
		URL               string          `json:"url"`
		Title             string          `json:"title"`
		HeadRefName       string          `json:"headRefName"`
		BaseRefName       string          `json:"baseRefName"`
		HeadRefOID        string          `json:"headRefOid"`
		IsDraft           bool            `json:"isDraft"`
		State             string          `json:"state"`
		MergedAt          *time.Time      `json:"mergedAt"`
		Mergeable         string          `json:"mergeable"`
		ReviewDecision    string          `json:"reviewDecision"`
		StatusCheckRollup json.RawMessage `json:"statusCheckRollup"`
		Author            struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return AttachedPR{}, err
	}
	repo, err := c.Repo(ctx)
	if err != nil {
		return AttachedPR{}, err
	}
	status := derivePRStatus(raw.State, raw.MergedAt, raw.Mergeable, raw.ReviewDecision, raw.IsDraft, raw.StatusCheckRollup)
	return AttachedPR{Owner: repo.Owner, Repo: repo.Name, Number: raw.Number, URL: raw.URL, Title: raw.Title, AuthorLogin: raw.Author.Login, HeadRef: raw.HeadRefName, BaseRef: raw.BaseRefName, HeadOID: raw.HeadRefOID, Status: status}, nil
}

type Repo struct {
	Owner string
	Name  string
}

func (c Client) Repo(ctx context.Context) (Repo, error) {
	b, err := c.run(ctx, "repo", "view", "--json", "owner,name")
	if err != nil {
		return Repo{}, err
	}
	var raw struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Repo{}, err
	}
	return Repo{Owner: raw.Owner.Login, Name: raw.Name}, nil
}

func (c Client) PRList(ctx context.Context) ([]PullRequest, error) {
	b, err := c.run(ctx, "pr", "list", "--state", "open", "--json", "number,title,url,author,headRefName,baseRefName,headRefOid,isDraft,updatedAt", "--limit", "30")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Number      int       `json:"number"`
		Title       string    `json:"title"`
		URL         string    `json:"url"`
		HeadRefName string    `json:"headRefName"`
		BaseRefName string    `json:"baseRefName"`
		HeadRefOID  string    `json:"headRefOid"`
		IsDraft     bool      `json:"isDraft"`
		UpdatedAt   time.Time `json:"updatedAt"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	prs := make([]PullRequest, 0, len(raw))
	for _, r := range raw {
		prs = append(prs, PullRequest{Number: r.Number, Title: r.Title, URL: r.URL, AuthorLogin: r.Author.Login, HeadRef: r.HeadRefName, BaseRef: r.BaseRefName, HeadOID: r.HeadRefOID, IsDraft: r.IsDraft, UpdatedAt: r.UpdatedAt})
	}
	return prs, nil
}

const threadsQuery = `
query($owner:String!, $name:String!, $number:Int!) {
  repository(owner:$owner, name:$name) {
    pullRequest(number:$number) {
      reviewThreads(first:100) {
        nodes {
          id
          isResolved
          isOutdated
          path
          line
          startLine
          side
          startSide
          comments(first:100) {
            nodes {
              id
              body
              url
              createdAt
              updatedAt
              author {
                login
                avatarUrl
                ... on User { name }
              }
            }
          }
        }
      }
    }
  }
}`

func (c Client) Threads(ctx context.Context, pr AttachedPR) ([]Thread, error) {
	b, err := c.run(ctx, "api", "graphql", "-f", "query="+threadsQuery, "-f", "owner="+pr.Owner, "-f", "name="+pr.Repo, "-F", fmt.Sprintf("number=%d", pr.Number))
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID        string `json:"id"`
							Resolved  bool   `json:"isResolved"`
							Outdated  bool   `json:"isOutdated"`
							Path      string `json:"path"`
							Line      int    `json:"line"`
							StartLine int    `json:"startLine"`
							Side      string `json:"side"`
							StartSide string `json:"startSide"`
							Comments  struct {
								Nodes []struct {
									ID        string    `json:"id"`
									Body      string    `json:"body"`
									URL       string    `json:"url"`
									CreatedAt time.Time `json:"createdAt"`
									UpdatedAt time.Time `json:"updatedAt"`
									Author    struct {
										Login     string `json:"login"`
										Name      string `json:"name"`
										AvatarURL string `json:"avatarUrl"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var threads []Thread
	for _, n := range raw.Data.Repository.PullRequest.ReviewThreads.Nodes {
		thread := Thread{ID: n.ID, Path: n.Path, Line: n.Line, StartLine: n.StartLine, Side: n.Side, StartSide: n.StartSide, Resolved: n.Resolved, Outdated: n.Outdated}
		for _, c := range n.Comments.Nodes {
			thread.Comments = append(thread.Comments, Comment{ID: c.ID, Body: c.Body, URL: c.URL, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Author: Author{Login: c.Author.Login, Name: c.Author.Name, AvatarURL: c.Author.AvatarURL}})
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if c.Runner == nil {
		c.Runner = ExecRunner{}
	}
	return c.Runner.Run(ctx, c.Dir, args...)
}
