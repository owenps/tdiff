package github

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner map[string]string

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	return []byte(f[key]), nil
}

func TestPRViewParsesStatus(t *testing.T) {
	client := Client{Runner: fakeRunner{
		"pr view --json " + prViewFields: `{"number":12,"title":"GitHub sync","url":"https://github.com/o/r/pull/12","headRefName":"gh","baseRefName":"main","headRefOid":"abc","isDraft":false,"state":"OPEN","mergeable":"MERGEABLE","reviewDecision":"APPROVED","statusCheckRollup":[{"conclusion":"SUCCESS","status":"COMPLETED"}],"author":{"login":"owenps"}}`,
		"repo view --json owner,name":    `{"name":"r","owner":{"login":"o"}}`,
	}}
	pr, err := client.PRView(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 12 || pr.Owner != "o" || pr.Repo != "r" || pr.Status != PRStatusReady {
		t.Fatalf("bad pr: %#v", pr)
	}
}

func TestPRListParsesGhOutput(t *testing.T) {
	client := Client{Runner: fakeRunner{
		"pr list --state open --json number,title,url,author,headRefName,baseRefName,headRefOid,isDraft,updatedAt --limit 30": `[{"number":12,"title":"GitHub sync","url":"https://github.com/o/r/pull/12","headRefName":"gh","baseRefName":"main","headRefOid":"abc","isDraft":true,"updatedAt":"2026-01-02T03:04:05Z","author":{"login":"owenps"}}]`,
	}}
	prs, err := client.PRList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 12 || prs[0].AuthorLogin != "owenps" || !prs[0].IsDraft {
		t.Fatalf("bad prs: %#v", prs)
	}
}

func TestFormatRunErrorCompactsGraphQLQuery(t *testing.T) {
	err := formatRunError([]string{"api", "graphql", "-f", "query=query { huge }", "-f", "owner=o"}, context.Canceled, "GraphQL: forbidden")
	got := err.Error()
	if strings.Contains(got, "query { huge }") || strings.Contains(got, "owner=o") || !strings.Contains(got, "gh api graphql failed") || !strings.Contains(got, "GraphQL: forbidden") {
		t.Fatalf("bad error: %s", got)
	}
}

func TestThreadsParsesGraphQL(t *testing.T) {
	client := Client{Runner: fakeRunner{
		"api graphql -f query=" + threadsQuery + " -f owner=o -f name=r -F number=12": `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"t1","isResolved":false,"isOutdated":false,"path":"main.go","line":7,"startLine":5,"side":"RIGHT","startSide":"RIGHT","comments":{"nodes":[{"id":"c1","body":"body","url":"u","createdAt":"2026-01-02T03:04:05Z","updatedAt":"2026-01-02T03:04:06Z","author":{"login":"owenps","avatarUrl":"a","name":"Owen"}}]}}]}}}}}`,
	}}
	threads, err := client.Threads(context.Background(), AttachedPR{Owner: "o", Repo: "r", Number: 12})
	if err != nil {
		t.Fatal(err)
	}
	want := []Thread{{ID: "t1", Path: "main.go", Line: 7, StartLine: 5, Side: "RIGHT", StartSide: "RIGHT", Comments: []Comment{{ID: "c1", Body: "body", URL: "u", Author: Author{Login: "owenps", Name: "Owen", AvatarURL: "a"}, CreatedAt: threads[0].Comments[0].CreatedAt, UpdatedAt: threads[0].Comments[0].UpdatedAt}}}}
	if !reflect.DeepEqual(threads, want) {
		t.Fatalf("threads = %#v\nwant %#v", threads, want)
	}
}
