package app

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	gh "github.com/owenps/tdiff/internal/github"
)

type prPicker struct {
	active         bool
	loading        bool
	err            string
	input          string
	index          int
	attachedNumber int
	candidates     []gh.PullRequest
}

func (p prPicker) Active() bool  { return p.active }
func (p prPicker) Loading() bool { return p.loading }

func (p *prPicker) Open() {
	p.active = true
	p.loading = true
	p.err = ""
	p.input = ""
	p.index = 0
	p.candidates = nil
}

func (p *prPicker) Close() {
	p.active = false
	p.loading = false
	p.err = ""
	p.input = ""
}

func (p *prPicker) SetLoaded(prs []gh.PullRequest, err error, attachedNumber int) {
	p.loading = false
	p.attachedNumber = attachedNumber
	p.candidates = prs
	if err != nil {
		p.err = "PR list failed; type number"
		return
	}
	p.err = ""
	for i, pr := range prs {
		if pr.Number == attachedNumber {
			p.index = i
			break
		}
	}
}

func (p *prPicker) UpdateKey(msg tea.KeyMsg) (int, bool, error) {
	switch msg.String() {
	case "esc":
		p.Close()
		return 0, false, nil
	case "up", "k":
		p.index = max(0, p.index-1)
		return 0, false, nil
	case "down", "j":
		p.index = min(max(0, len(p.filtered())-1), p.index+1)
		return 0, false, nil
	case "backspace":
		if len(p.input) > 0 {
			p.input = p.input[:len(p.input)-1]
			p.index = 0
		}
		return 0, false, nil
	case "enter":
		number, err := p.selectedNumber()
		if err != nil {
			return 0, false, err
		}
		p.Close()
		return number, true, nil
	}
	for _, r := range msg.Runes {
		if r >= 32 && r != 127 {
			p.input += string(r)
			p.index = 0
		}
	}
	return 0, false, nil
}

func (p prPicker) selectedNumber() (int, error) {
	input := strings.TrimSpace(p.input)
	if n, ok := parsePRNumber(input); ok {
		return n, nil
	}
	filtered := p.filtered()
	if len(filtered) == 0 {
		return 0, fmt.Errorf("no PR selected")
	}
	idx := clamp(p.index, 0, len(filtered)-1)
	return filtered[idx].Number, nil
}

func parsePRNumber(input string) (int, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, false
	}
	if strings.Contains(input, "/pull/") {
		parts := strings.Split(strings.TrimRight(input, "/"), "/")
		input = parts[len(parts)-1]
	} else if strings.Contains(input, "#") {
		parts := strings.Split(input, "#")
		input = parts[len(parts)-1]
	}
	n, err := strconv.Atoi(input)
	return n, err == nil && n > 0
}

func (p prPicker) filtered() []gh.PullRequest {
	q := strings.ToLower(strings.TrimSpace(p.input))
	if q == "" {
		return p.candidates
	}
	var out []gh.PullRequest
	for _, pr := range p.candidates {
		haystack := strings.ToLower(fmt.Sprintf("%d %s %s %s", pr.Number, pr.Title, pr.AuthorLogin, pr.HeadRef))
		if strings.Contains(haystack, q) {
			out = append(out, pr)
		}
	}
	return out
}

func (p prPicker) View(width, loadingFrame int) string {
	filtered := p.filtered()
	w := max(20, width)
	rows := []string{fmt.Sprintf("# attach PR: %s", p.input)}
	if p.loading {
		frame := loadingSpinnerFrames[loadingFrame%len(loadingSpinnerFrames)]
		rows = append(rows, dimStyle.Render(frame+" loading PRs…"))
	} else if p.err != "" {
		rows = append(rows, errorStyle.Render("  "+p.err))
	} else if len(filtered) == 0 {
		rows = append(rows, dimStyle.Render("  no matching PRs"))
	} else {
		limit := min(5, len(filtered))
		start := clamp(p.index-limit/2, 0, max(0, len(filtered)-limit))
		for i := start; i < start+limit && i < len(filtered); i++ {
			pr := filtered[i]
			prefix := "  "
			style := dimStyle
			if i == p.index {
				prefix = "▌ "
				style = selectedStyle
			}
			draft := ""
			if pr.IsDraft {
				draft = " draft"
			}
			current := ""
			if p.attachedNumber == pr.Number {
				current = " current"
			}
			line := fmt.Sprintf("%s#%d %s  %s  %s%s%s", prefix, pr.Number, truncate(pr.Title, max(10, w-44)), pr.AuthorLogin, pr.HeadRef, draft, current)
			rows = append(rows, style.Render(truncate(line, w)))
		}
	}
	rows = append(rows, dimStyle.Render("enter attach · esc cancel"))
	return strings.Join(rows, "\n")
}
