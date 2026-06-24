package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	gh "github.com/owenps/tdiff/internal/github"
	"github.com/owenps/tdiff/internal/thread"
	"github.com/owenps/tdiff/internal/threadworkflow"
)

func (m Model) handleAsyncMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case loadingSpinnerTickMsg:
		if !m.ready() || m.prPicker.Loading() || m.prAttaching || m.refreshing {
			m.loadingFrame++
			return m, loadingSpinnerTick(), true
		}
		return m, nil, true
	case autoRefreshTickMsg:
		cmd := autoRefreshTick()
		if m.shouldAutoRefresh() {
			m.refreshing = true
			cmd = tea.Batch(cmd, m.refreshProjectCmd(true), loadingSpinnerTick())
		}
		return m, cmd, true
	case prListLoadedMsg:
		if !m.prPicker.Active() {
			return m, nil, true
		}
		attachedNumber := 0
		if m.store != nil && m.store.GitHub != nil {
			attachedNumber = m.store.GitHub.Number
		}
		m.prPicker.SetLoaded(msg.prs, msg.err, attachedNumber)
		return m, nil, true
	case prAttachLoadedMsg:
		previousStatus := m.status
		m.handlePRAttachLoaded(msg)
		return m, m.statusToastCmd(previousStatus), true
	case refreshLoadedMsg:
		previousStatus := m.status
		m.handleRefreshLoaded(msg)
		return m, m.statusToastCmd(previousStatus), true
	case threadStatusChangedMsg:
		previousStatus := m.status
		m.handleThreadStatusChanged(msg)
		return m, m.statusToastCmd(previousStatus), true
	case clearStatusMsg:
		if msg.id == m.statusID {
			m.status = ""
		}
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m *Model) handlePRAttachLoaded(msg prAttachLoadedMsg) {
	m.prAttaching = false
	if msg.err != nil {
		m.status = msg.err.Error()
		return
	}
	if err := m.store.AttachGitHubPR(msg.pr); err != nil {
		m.status = err.Error()
		return
	}
	if msg.threadErr != nil {
		m.logDebug("attached PR #%d sync failed: %v", msg.pr.Number, msg.threadErr)
		m.status = fmt.Sprintf("attached PR #%d · sync failed", msg.pr.Number)
		return
	}
	count, err := m.store.SyncGitHubThreads(msg.pr, msg.threads)
	if err != nil {
		m.status = err.Error()
		return
	}
	m.session.RefreshFilters()
	m.invalidateViewCache()
	m.status = fmt.Sprintf("attached PR #%d · %d threads", msg.pr.Number, count)
}

func (m *Model) handleRefreshLoaded(msg refreshLoadedMsg) {
	m.refreshing = false
	if msg.err != nil {
		if msg.auto {
			m.logDebug("auto refresh failed: %v", msg.err)
			return
		}
		m.status = msg.err.Error()
		return
	}
	if err := m.reloadStore(); err != nil {
		if msg.auto {
			m.logDebug("auto store reload failed: %v", err)
			return
		}
		m.status = err.Error()
		return
	}
	anchor := m.cursorAnchor()
	m.compareTarget = msg.compareTarget
	m.updateChangedFiles(msg.snap.Files)
	m.session.SetSnapshot(msg.snap.Files, msg.snap.Hash)
	m.restoreCursor(anchor)
	m.viewCache = make(map[string]string)
	m.statsCache = make(map[string]diffStats)
	m.syntaxCache = make(map[string]string)
	m.splitHunkCache = make(map[string]map[string]bool)
	m.splitNavCache = make(map[string]splitNav)
	m.splitOffset = 0
	if msg.offline {
		if !msg.auto {
			m.status = "diff refreshed · offline"
		}
		return
	}
	if msg.noPR || msg.pr == nil {
		if !msg.auto {
			m.status = "diff refreshed · no PR"
		}
		return
	}
	if err := m.store.AttachGitHubPR(*msg.pr); err != nil {
		if msg.auto {
			m.logDebug("auto refresh attach failed: %v", err)
			return
		}
		m.status = err.Error()
		return
	}
	if msg.threadErr != nil {
		m.logDebug("github sync failed: %v", msg.threadErr)
		if !msg.auto {
			m.status = "diff refreshed · github sync failed"
		}
		return
	}
	count, err := m.store.SyncGitHubThreads(*msg.pr, msg.threads)
	if err != nil {
		if msg.auto {
			m.logDebug("auto refresh sync failed: %v", err)
			return
		}
		m.status = err.Error()
		return
	}
	m.session.RefreshFilters()
	m.invalidateViewCache()
	if !msg.auto {
		m.status = fmt.Sprintf("PR #%d synced · %d threads", msg.pr.Number, count)
	}
}

func (m Model) updateComposer(msg tea.Msg) (tea.Model, tea.Cmd) {
	previousStatus := m.status
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "alt+enter":
			if err := m.saveThread(); err != nil {
				m.status = err.Error()
			} else {
				m.status = "thread saved"
				m.invalidateViewCache()
				m.closeComposer()
			}
			return m, m.statusToastCmd(previousStatus)
		case "esc":
			m.closeComposer()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	return m, cmd
}

func (m *Model) closeComposer() {
	m.composing = false
	m.composerBaseView = ""
	m.session.CancelRange()
	m.editingThreadID = ""
	m.replyingThreadID = ""
	m.pendingTarget = threadworkflow.Target{}
	m.editor.Blur()
	m.editor.Reset()
}

func (m *Model) updateWindowSize(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height
	m.editor.SetWidth(max(20, msg.Width-32))
	m.ensureCursorVisible()
}

func (m Model) updateKey(msg tea.KeyMsg, previousStatus string) (tea.Model, tea.Cmd) {
	if m.prPicker.Active() {
		return m.updatePRPicker(msg)
	}
	if m.jumpPrompt {
		return m.updateJumpPrompt(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "?":
		m.pendingKey = ""
		m.showHelp = !m.showHelp
		m.ensureCursorVisible()
	case ":":
		m.pendingKey = ""
		m.jumpPrompt = true
		m.jumpInput = ""
	case "#":
		m.pendingKey = ""
		return m, m.openPRPicker()
	case "g":
		if m.pendingKey == "g" {
			m.pendingKey = ""
			m.jumpTop()
		} else {
			m.pendingKey = "g"
		}
	case "G":
		m.pendingKey = ""
		m.jumpBottom()
	case "[", "]":
		m.pendingKey = msg.String()
	case "h":
		if m.pendingKey == "[" {
			m.pendingKey = ""
			m.prevHunk()
		} else if m.pendingKey == "]" {
			m.pendingKey = ""
			m.nextHunk()
		}
	case "j", "down":
		m.pendingKey = ""
		m.moveLine(1)
	case "k", "up":
		m.pendingKey = ""
		m.moveLine(-1)
	case "n", "right":
		m.pendingKey = ""
		m.session.MoveFile(1)
		m.acknowledgeCurrentFileChange()
		m.invalidateViewCache()
		m.splitOffset = 0
	case "p", "left":
		m.pendingKey = ""
		m.session.MoveFile(-1)
		m.acknowledgeCurrentFileChange()
		m.invalidateViewCache()
		m.splitOffset = 0
	case "s":
		m.pendingKey = ""
		m.split = !m.split
		m.ensureSplitCursorVisible(m.bodyHeight())
	case "x":
		m.pendingKey = ""
		m.syntax = !m.syntax
		m.status = fmt.Sprintf("syntax: %t", m.syntax)
	case "c":
		m.pendingKey = ""
		m.contextDim = !m.contextDim
		m.status = fmt.Sprintf("context dim: %t", m.contextDim)
	case "u":
		m.pendingKey = ""
		m.status = fmt.Sprintf("hide viewed: %t", m.session.ToggleHideViewed())
		m.invalidateViewCache()
	case "m":
		m.pendingKey = ""
		m.status = fmt.Sprintf("threads only: %t", m.session.ToggleThreadsOnly())
		m.invalidateViewCache()
	case "b":
		m.pendingKey = ""
		m.hideSidebar = !m.hideSidebar
		m.status = fmt.Sprintf("sidebar: %t", !m.hideSidebar)
	case "i":
		m.pendingKey = ""
		m.hideInlineThreads = !m.hideInlineThreads
		m.status = fmt.Sprintf("inline threads: %t", !m.hideInlineThreads)
	case "y":
		m.copySelectedThread()
	case "Y":
		m.copyAllThreads()
	case "w":
		m.pendingKey = ""
		m.wrapCursorLine = !m.wrapCursorLine
		m.ensureSplitCursorVisible(m.bodyHeight())
		m.status = fmt.Sprintf("wrap cursor line: %t", m.wrapCursorLine)
	case "L":
		m.pendingKey = ""
		m.hideLineNumbers = !m.hideLineNumbers
		m.status = fmt.Sprintf("line numbers: %t", !m.hideLineNumbers)
	case "W":
		m.toggleWhitespace()
	case "R":
		return m.refreshKey(previousStatus)
	case "A":
		m.approveReview()
	case "v":
		m.toggleViewedStatus()
	case "z":
		return m.toggleThreadResolvedKey(previousStatus)
	case "r":
		m.toggleRangeStatus()
	case "a", "t":
		return m.threadKey(previousStatus)
	case "enter":
		return m.replyThreadKey(previousStatus)
	case "e":
		return m.editThreadKey(previousStatus)
	case "d":
		m.deleteThreadKey()
	}
	return m, m.statusToastCmd(previousStatus)
}

func (m *Model) copySelectedThread() {
	m.pendingKey = ""
	if selected, ok := m.selectedThread(); ok {
		if err := clipboard.WriteAll(threadText(selected)); err != nil {
			m.status = err.Error()
		} else {
			m.status = "thread copied"
		}
	} else {
		m.status = "no thread on selected line"
	}
}

func (m *Model) copyAllThreads() {
	m.pendingKey = ""
	b, err := json.MarshalIndent(m.store.Threads, "", "  ")
	if err != nil {
		m.status = err.Error()
		return
	}
	if err := clipboard.WriteAll(string(append(b, '\n'))); err != nil {
		m.status = err.Error()
	} else {
		m.status = "threads copied"
	}
}

func (m *Model) toggleWhitespace() {
	m.pendingKey = ""
	m.cfg.IgnoreWhitespace = !m.cfg.IgnoreWhitespace
	if err := m.reload(context.Background()); err != nil {
		m.status = err.Error()
	} else {
		m.status = fmt.Sprintf("ignore whitespace: %t", m.cfg.IgnoreWhitespace)
	}
}

func (m Model) refreshKey(previousStatus string) (tea.Model, tea.Cmd) {
	m.pendingKey = ""
	if m.refreshing {
		m.status = "refreshing…"
		return m, m.statusToastCmd(previousStatus)
	}
	m.refreshing = true
	m.status = "refreshing…"
	return m, tea.Batch(m.refreshProjectCmd(false), loadingSpinnerTick())
}

func (m *Model) toggleViewedStatus() {
	m.pendingKey = ""
	result, err := m.session.ToggleViewed()
	if err != nil {
		m.status = err.Error()
		return
	}
	if result.Path == "" {
		return
	}
	m.acknowledgeFileChange(result.Path)
	m.invalidateViewCache()
	if !result.Viewed {
		m.status = "unmarked viewed"
	} else if m.session.HideViewed() || result.Advanced {
		m.status = "marked viewed"
	} else {
		m.status = "marked viewed; no next unviewed file"
	}
}

func (m *Model) approveReview() {
	m.pendingKey = ""
	if m.store.ReviewStatus(m.session.DiffHash()) == "approved" {
		if err := m.store.Unapprove(m.session.DiffHash()); err != nil {
			m.status = err.Error()
			return
		}
		m.status = "review approval removed"
		return
	}
	if open := len(m.session.ThreadPositions()); open > 0 {
		m.status = fmt.Sprintf("cannot approve: %d open threads", open)
		return
	}
	if err := m.store.Approve(m.session.DiffHash()); err != nil {
		m.status = err.Error()
		return
	}
	m.status = "review approved"
}

func (m Model) toggleThreadResolvedKey(previousStatus string) (tea.Model, tea.Cmd) {
	m.pendingKey = ""
	selected, ok := m.selectedThread()
	if !ok {
		m.status = "no thread on selected line"
		return m, m.statusToastCmd(previousStatus)
	}
	next := thread.StatusResolved
	label := "resolved"
	if selected.Status == thread.StatusResolved {
		next = thread.StatusOpen
		label = "reopened"
	}
	if selected.Source != thread.SourceGitHub {
		if err := m.setThreadStatus(selected.ID, next); err != nil {
			m.status = err.Error()
		} else {
			m.status = "thread " + label
		}
		return m, m.statusToastCmd(previousStatus)
	}
	if selected.GitHub == nil || selected.GitHub.ThreadID == "" {
		m.status = "github thread missing id"
		return m, m.statusToastCmd(previousStatus)
	}
	m.status = "thread " + label + "…"
	threadID := selected.GitHub.ThreadID
	localID := selected.ID
	repoRoot := m.repo.Root
	return m, func() tea.Msg {
		client := gh.NewClient(repoRoot)
		var err error
		if next == thread.StatusResolved {
			err = client.ResolveThread(context.Background(), threadID)
		} else {
			err = client.UnresolveThread(context.Background(), threadID)
		}
		return threadStatusChangedMsg{id: localID, status: next, err: err}
	}
}

func (m *Model) handleThreadStatusChanged(msg threadStatusChangedMsg) {
	if msg.err != nil {
		m.logDebug("github thread status failed: %v", msg.err)
		m.status = "github thread status failed"
		return
	}
	if err := m.setThreadStatus(msg.id, msg.status); err != nil {
		m.status = err.Error()
		return
	}
	if msg.status == thread.StatusResolved {
		m.status = "thread resolved"
	} else {
		m.status = "thread reopened"
	}
}

func (m *Model) setThreadStatus(id string, status thread.Status) error {
	if status == thread.StatusResolved {
		if err := m.store.Resolve(id, thread.ActorHuman); err != nil {
			return err
		}
	} else {
		if err := m.store.Reopen(id, thread.ActorHuman); err != nil {
			return err
		}
	}
	m.session.RefreshFilters()
	m.invalidateViewCache()
	return nil
}

func (m *Model) toggleRangeStatus() {
	m.pendingKey = ""
	result := m.session.ToggleRange()
	if result.Cancelled {
		m.status = "range cancelled"
	} else if result.Started {
		m.status = "range started; move then press a"
	} else {
		m.status = "range must start on a diff line"
	}
}

func (m Model) threadKey(previousStatus string) (tea.Model, tea.Cmd) {
	if m.pendingKey == "[" {
		m.pendingKey = ""
		m.prevThread()
		return m, m.statusToastCmd(previousStatus)
	}
	if m.pendingKey == "]" {
		m.pendingKey = ""
		m.nextThread()
		return m, m.statusToastCmd(previousStatus)
	}
	m.pendingKey = ""
	if m.session.RangeActive() {
		target, err := m.rangeTarget()
		if err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		m.startNewThread(target)
		return m, textarea.Blink
	}
	if selected, ok := m.selectedThread(); ok {
		if selected.Source == thread.SourceGitHub {
			m.status = "github thread readonly"
			return m, m.statusToastCmd(previousStatus)
		}
		if err := m.startEditThread(selected); err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		return m, textarea.Blink
	}
	if target, err := m.singleLineTarget(); err == nil {
		m.startNewThread(target)
		return m, textarea.Blink
	}
	return m, m.statusToastCmd(previousStatus)
}

func (m Model) replyThreadKey(previousStatus string) (tea.Model, tea.Cmd) {
	m.pendingKey = ""
	if selected, ok := m.selectedThread(); ok {
		if selected.Source == thread.SourceGitHub {
			m.status = "github thread readonly"
			return m, m.statusToastCmd(previousStatus)
		}
		m.startReplyThread(selected)
		return m, textarea.Blink
	}
	m.status = "no thread on selected line"
	return m, m.statusToastCmd(previousStatus)
}

func (m Model) editThreadKey(previousStatus string) (tea.Model, tea.Cmd) {
	m.pendingKey = ""
	if selected, ok := m.selectedThread(); ok {
		if selected.Source == thread.SourceGitHub {
			m.status = "github thread readonly"
			return m, m.statusToastCmd(previousStatus)
		}
		if err := m.startEditThread(selected); err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		return m, textarea.Blink
	}
	m.status = "no thread on selected line"
	return m, m.statusToastCmd(previousStatus)
}

func (m *Model) deleteThreadKey() {
	m.pendingKey = ""
	if selected, ok := m.selectedThread(); ok {
		if selected.Source == thread.SourceGitHub {
			m.status = "github thread readonly"
			return
		}
		if err := m.threads.Delete(selected.ID); err != nil {
			m.status = err.Error()
		} else {
			m.status = "thread deleted"
			m.invalidateViewCache()
			if m.session.ThreadsOnly() {
				m.session.RefreshFilters()
			}
		}
		return
	}
	m.status = "no thread on selected line"
}
