package app

import (
	"context"
	"fmt"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/owenps/tdiff/internal/annotate"
	"github.com/owenps/tdiff/internal/annotations"
)

func (m Model) handleAsyncMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case loadingSpinnerTickMsg:
		if !m.ready() || m.prPicker.Loading() || m.prAttaching || m.refreshing {
			m.loadingFrame++
			return m, loadingSpinnerTick(), true
		}
		return m, nil, true
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
		m.status = fmt.Sprintf("attached PR #%d · sync failed", msg.pr.Number)
		return
	}
	count, err := m.store.SyncGitHubAnnotations(msg.pr, msg.threads)
	if err != nil {
		m.status = err.Error()
		return
	}
	m.session.RefreshFilters()
	m.status = fmt.Sprintf("attached PR #%d · %d annotations", msg.pr.Number, count)
}

func (m *Model) handleRefreshLoaded(msg refreshLoadedMsg) {
	m.refreshing = false
	if msg.err != nil {
		m.status = msg.err.Error()
		return
	}
	m.compareTarget = msg.compareTarget
	m.session.SetSnapshot(msg.snap.Files, msg.snap.Hash)
	m.syntaxCache = make(map[string]string)
	m.splitHunkCache = make(map[string]map[string]bool)
	m.splitNavCache = make(map[string]splitNav)
	m.splitOffset = 0
	if msg.offline {
		m.status = "diff refreshed · offline"
		return
	}
	if msg.noPR || msg.pr == nil {
		m.status = "diff refreshed · no PR"
		return
	}
	if err := m.store.AttachGitHubPR(*msg.pr); err != nil {
		m.status = err.Error()
		return
	}
	if msg.threadErr != nil {
		m.status = "diff refreshed · github sync failed"
		return
	}
	count, err := m.store.SyncGitHubAnnotations(*msg.pr, msg.threads)
	if err != nil {
		m.status = err.Error()
		return
	}
	m.session.RefreshFilters()
	m.status = fmt.Sprintf("PR #%d synced · %d annotations", msg.pr.Number, count)
}

func (m Model) updateComposer(msg tea.Msg) (tea.Model, tea.Cmd) {
	previousStatus := m.status
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "alt+enter":
			if err := m.saveAnnotation(); err != nil {
				m.status = err.Error()
			} else {
				m.status = "annotation saved"
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
	m.editingAnnotationID = ""
	m.pendingTarget = annotations.Target{}
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
		m.splitOffset = 0
	case "p", "left":
		m.pendingKey = ""
		m.session.MoveFile(-1)
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
	case "m":
		m.pendingKey = ""
		m.status = fmt.Sprintf("annotations only: %t", m.session.ToggleAnnotationsOnly())
	case "b":
		m.pendingKey = ""
		m.hideSidebar = !m.hideSidebar
		m.status = fmt.Sprintf("sidebar: %t", !m.hideSidebar)
	case "y":
		m.copySelectedAnnotation()
	case "Y":
		m.copyAllAnnotations()
	case "w":
		m.pendingKey = ""
		m.wrapCursorLine = !m.wrapCursorLine
		m.status = fmt.Sprintf("wrap cursor line: %t", m.wrapCursorLine)
	case "L":
		m.pendingKey = ""
		m.hideLineNumbers = !m.hideLineNumbers
		m.status = fmt.Sprintf("line numbers: %t", !m.hideLineNumbers)
	case "W":
		m.toggleWhitespace()
	case "R":
		return m.refreshKey(previousStatus)
	case "v":
		m.toggleViewedStatus()
	case "r":
		m.toggleRangeStatus()
	case "a":
		return m.annotationKey(previousStatus)
	case "e":
		return m.editAnnotationKey(previousStatus)
	case "d":
		m.deleteAnnotationKey()
	}
	return m, m.statusToastCmd(previousStatus)
}

func (m *Model) copySelectedAnnotation() {
	m.pendingKey = ""
	if annotation, ok := m.selectedAnnotation(); ok {
		if err := clipboard.WriteAll(annotationMarkdown(annotation)); err != nil {
			m.status = err.Error()
		} else {
			m.status = "annotation copied"
		}
	} else {
		m.status = "no annotation on selected line"
	}
}

func (m *Model) copyAllAnnotations() {
	m.pendingKey = ""
	if err := clipboard.WriteAll(m.store.ExportMarkdown()); err != nil {
		m.status = err.Error()
	} else {
		m.status = "annotations copied"
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
	return m, tea.Batch(m.refreshProjectCmd(), loadingSpinnerTick())
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
	if !result.Viewed {
		m.status = "unmarked viewed"
	} else if m.session.HideViewed() || result.Advanced {
		m.status = "marked viewed"
	} else {
		m.status = "marked viewed; no next unviewed file"
	}
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

func (m Model) annotationKey(previousStatus string) (tea.Model, tea.Cmd) {
	if m.pendingKey == "[" {
		m.pendingKey = ""
		m.prevAnnotation()
		return m, m.statusToastCmd(previousStatus)
	}
	if m.pendingKey == "]" {
		m.pendingKey = ""
		m.nextAnnotation()
		return m, m.statusToastCmd(previousStatus)
	}
	m.pendingKey = ""
	if m.session.RangeActive() {
		target, err := m.rangeTarget()
		if err != nil {
			m.status = err.Error()
			return m, m.statusToastCmd(previousStatus)
		}
		m.startNewAnnotation(target)
		return m, textarea.Blink
	}
	if annotation, ok := m.selectedAnnotation(); ok {
		if annotation.Source == annotate.SourceGitHub {
			m.status = "github annotation readonly"
			return m, m.statusToastCmd(previousStatus)
		}
		m.startEditAnnotation(annotation)
		return m, textarea.Blink
	}
	if target, err := m.singleLineTarget(); err == nil {
		m.startNewAnnotation(target)
		return m, textarea.Blink
	}
	return m, m.statusToastCmd(previousStatus)
}

func (m Model) editAnnotationKey(previousStatus string) (tea.Model, tea.Cmd) {
	m.pendingKey = ""
	if annotation, ok := m.selectedAnnotation(); ok {
		if annotation.Source == annotate.SourceGitHub {
			m.status = "github annotation readonly"
			return m, m.statusToastCmd(previousStatus)
		}
		m.startEditAnnotation(annotation)
		return m, textarea.Blink
	}
	m.status = "no annotation on selected line"
	return m, m.statusToastCmd(previousStatus)
}

func (m *Model) deleteAnnotationKey() {
	m.pendingKey = ""
	if annotation, ok := m.selectedAnnotation(); ok {
		if annotation.Source == annotate.SourceGitHub {
			m.status = "github annotation readonly"
			return
		}
		if err := m.annotations.Delete(annotation.ID); err != nil {
			m.status = err.Error()
		} else {
			m.status = "annotation deleted"
			if m.session.AnnotationsOnly() {
				m.session.RefreshFilters()
			}
		}
		return
	}
	m.status = "no annotation on selected line"
}
