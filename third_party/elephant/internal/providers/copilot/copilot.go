package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "embed"

	"al.essio.dev/pkg/shellescape"
	assert "github.com/ZanzyTHEbar/assert-lib"
	errbuilder "github.com/ZanzyTHEbar/errbuilder-go"
	"github.com/abenz1267/elephant/v2/pkg/common"
	"github.com/abenz1267/elephant/v2/pkg/pb/pb"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var (
	Name       = "copilot"
	NamePretty = "Copilot"
)

const (
	ActionSend            = "send"
	ActionAsk             = "ask"
	ActionCopyAnswer      = "copy_answer"
	ActionCopyError       = "copy_error"
	ActionCopyCommand     = "copy_command"
	ActionCopyAll         = "copy_all_commands"
	ActionCopyTranscript  = "copy_transcript"
	ActionEditTranscript  = "edit_transcript"
	ActionApplyTranscript = "apply_transcript"
	ActionOpenTerminal    = "open_terminal"
	ActionNewSession      = "new_session"
	ActionNewTemp         = "new_temp_session"
	ActionSelectSession   = "select_session"
	ActionDeleteSession   = "delete_session"
	ActionClearSession    = "clear_session"
	ActionSetModel        = "set_model"
	ActionSetCliMode      = "set_cli_mode"
	ActionModeChat        = "mode_chat"
	ActionModeModels      = "mode_models"
	ActionModeSessions    = "mode_sessions"
	ActionTogglePersist   = "toggle_persist"
	ActionTogglePin       = "toggle_pin"
	ActionRenameSession   = "rename_session"
)

const (
	identifierSep = "::"
)

var defaultModels = []string{
	"claude-sonnet-4.5",
	"claude-haiku-4.5",
	"claude-opus-4.5",
	"claude-sonnet-4",
	"gemini-3-pro-preview",
	"gpt-5.2-codex",
	"gpt-5.2",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex",
	"gpt-5.1",
	"gpt-5",
	"gpt-5.1-codex-mini",
	"gpt-5-mini",
	"gpt-4.1",
}

const defaultSystemPrompt = "You are a helpful command-line assistant. Keep answers concise. When suggesting commands, put them in fenced code blocks."
const defaultTerminalPrefill = "bash -lc 'read -e -i %CMD% -p \">>> \" cmd; exec $SHELL'"

type Config struct {
	common.Config `koanf:",squash"`

	Enabled             bool     `koanf:"enabled" desc:"enable the copilot provider" default:"false"`
	CliMode             string   `koanf:"cli_mode" desc:"auto|copilot|gh|both" default:"auto"`
	DefaultModel        string   `koanf:"default_model" desc:"default model name" default:"claude-sonnet-4.5"`
	Models              []string `koanf:"models" desc:"available models" default:""`
	CopilotArgs         []string `koanf:"copilot_args" desc:"extra args for copilot CLI" default:""`
	GhCopilotArgs       []string `koanf:"gh_copilot_args" desc:"extra args for gh copilot wrapper" default:""`
	ClipboardCommand    string   `koanf:"clipboard_cmd" desc:"command used to copy text to clipboard" default:"wl-copy"`
	SessionDir          string   `koanf:"session_dir" desc:"session storage directory" default:""`
	MaxContextMessages  int      `koanf:"max_context_messages" desc:"max messages to include in prompt" default:"12"`
	MaxStoredMessages   int      `koanf:"max_stored_messages" desc:"max messages stored per session" default:"200"`
	MaxSessions         int      `koanf:"max_sessions" desc:"max persistent sessions on disk" default:"50"`
	CommandExtractRegex string   `koanf:"command_extract_regex" desc:"optional regex to extract commands from answers" default:""`
	TerminalPrefillCmd  string   `koanf:"terminal_prefill_cmd" desc:"command used to open a terminal with a prefilled command; %CMD% placeholder" default:""`
	AskPrefix           string   `koanf:"ask_prefix" desc:"label prefix for ask entry" default:"Ask Copilot:"`
	SystemPrompt        string   `koanf:"system_prompt" desc:"system prompt prepended to every query" default:""`
}

type CopilotState struct {
	CurrentSessionID         string `json:"current_session_id"`
	CurrentSessionPersistent bool   `json:"current_session_persistent"`
	CurrentModel             string `json:"current_model"`
	CurrentCliMode           string `json:"current_cli_mode"`
	CurrentMode              string `json:"current_mode"`
}

type Message struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

type Session struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Persistent   bool      `json:"persistent"`
	Pinned       bool      `json:"pinned"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Messages     []Message `json:"messages"`
	LastAnswer   string    `json:"last_answer"`
	LastCommands []string  `json:"last_commands"`
	LastError    string    `json:"last_error"`
}

//go:embed README.md
var readme string

var (
	config          *Config
	state           *CopilotState
	sessions        = map[string]*Session{}
	ephemeral       *Session
	mu              sync.Mutex
	configOnce      sync.Once
	logger          = logrus.New()
	commandRegex    *regexp.Regexp
	commandRegexErr error
	lastModeChange  time.Time
)

func Setup() {
	ensureConfig()

	loadState()
	loadSessions()
	applyViperOverrides()
	ensureDefaultModel()
	ensureDefaultMode()

	ctx := context.TODO()
	assert.Assert(ctx, config.DefaultModel != "", "default_model should not be empty")
	assert.Assert(ctx, len(config.Models) > 0, "models should not be empty")

	if config.CommandExtractRegex != "" {
		commandRegex, commandRegexErr = regexp.Compile(config.CommandExtractRegex)
		if commandRegexErr != nil {
			logger.WithError(commandRegexErr).Warn("invalid command_extract_regex; falling back to heuristic extraction")
		}
	}
}

func ensureConfig() {
	configOnce.Do(initConfig)
}

func initConfig() {
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.InfoLevel)

	config = &Config{
		Config: common.Config{
			Icon:     "help-browser",
			MinScore: 30,
		},
		Enabled:            false,
		CliMode:            "auto",
		DefaultModel:       "claude-sonnet-4.5",
		Models:             append([]string{}, defaultModels...),
		CopilotArgs:        []string{"--no-color", "--silent"},
		GhCopilotArgs:      []string{"--no-color", "--silent"},
		ClipboardCommand:   "wl-copy",
		MaxContextMessages: 12,
		MaxStoredMessages:  200,
		MaxSessions:        50,
		AskPrefix:          "Ask Copilot:",
		SystemPrompt:       defaultSystemPrompt,
		TerminalPrefillCmd: defaultTerminalPrefill,
	}

	common.LoadConfig(Name, config)

	if config.NamePretty != "" {
		NamePretty = config.NamePretty
	}
	if config.SessionDir == "" {
		config.SessionDir = defaultSessionDir()
	}
	if len(config.Models) == 0 {
		config.Models = append([]string{}, defaultModels...)
	}
	if config.SystemPrompt == "" {
		config.SystemPrompt = defaultSystemPrompt
	}
	if config.TerminalPrefillCmd == "" {
		config.TerminalPrefillCmd = defaultTerminalPrefill
	}

	state = &CopilotState{
		CurrentModel:   config.DefaultModel,
		CurrentCliMode: "copilot",
		CurrentMode:    "chat",
	}
	lastModeChange = time.Now()
}

func Available() bool {
	ensureConfig()
	if config == nil || !config.Enabled {
		return false
	}
	return effectiveCliMode() != ""
}

func PrintDoc() {
	fmt.Println(readme)
}

func Query(_ net.Conn, query string, single bool, exact bool, _ uint8) []*pb.QueryResponse_Item {
	query = strings.TrimSpace(query)
	entries := []*pb.QueryResponse_Item{}

	if query == "" && currentMode() != "chat" {
		if time.Since(lastModeChange) > 3*time.Second {
			setMode("chat")
		}
	}

	switch currentMode() {
	case "models":
		entries = append(entries, buildModelsMenu(query, exact)...)
	case "sessions":
		entries = append(entries, buildSessionsMenu(query, exact)...)
	default:
		entries = append(entries, buildChatEntries(query, exact)...)
	}

	if single && len(entries) == 0 {
		entries = append(entries, buildChatEntries(query, exact)...)
	}

	return entries
}

func Activate(_ bool, identifier, action, query, _ string, _ uint8, _ net.Conn) {
	switch action {
	case ActionSend:
		fallthrough
	case ActionAsk:
		askQuery := strings.TrimSpace(query)
		if askQuery == "" {
			return
		}
		if err := askCopilot(askQuery); err != nil {
			logger.WithError(err).Error("copilot ask failed")
		}
	case ActionCopyAnswer:
		session := currentSession()
		if session == nil {
			return
		}
		if err := copyToClipboard(session.LastAnswer); err != nil {
			logger.WithError(err).Error("copy answer failed")
		}
	case ActionCopyError:
		session := currentSession()
		if session == nil {
			return
		}
		if err := copyToClipboard(session.LastError); err != nil {
			logger.WithError(err).Error("copy error failed")
		}
	case ActionCopyCommand:
		cmd := commandByIdentifier(identifier)
		if cmd == "" {
			return
		}
		if err := copyToClipboard(cmd); err != nil {
			logger.WithError(err).Error("copy command failed")
		}
	case ActionCopyAll:
		session := currentSession()
		if session == nil || len(session.LastCommands) == 0 {
			return
		}
		if err := copyToClipboard(strings.Join(session.LastCommands, "\n")); err != nil {
			logger.WithError(err).Error("copy all commands failed")
		}
	case ActionCopyTranscript:
		if err := copyTranscript(); err != nil {
			logger.WithError(err).Error("copy transcript failed")
		}
	case ActionEditTranscript:
		if err := editTranscript(); err != nil {
			logger.WithError(err).Error("edit transcript failed")
		}
	case ActionApplyTranscript:
		if err := applyTranscriptEdits(); err != nil {
			logger.WithError(err).Error("apply transcript failed")
		}
	case ActionOpenTerminal:
		cmd := commandByIdentifier(identifier)
		if cmd == "" {
			return
		}
		if err := openTerminalPrefill(cmd); err != nil {
			logger.WithError(err).Error("open terminal failed")
		}
	case ActionNewSession:
		session := newSession(true)
		setCurrentSession(session)
		setMode("chat")
	case ActionNewTemp:
		session := newSession(false)
		setCurrentSession(session)
		setMode("chat")
	case ActionSelectSession:
		kind, value := splitIdentifier(identifier)
		if kind != "session" {
			return
		}
		selectSession(value)
		setMode("chat")
	case ActionDeleteSession:
		kind, value := splitIdentifier(identifier)
		if kind != "session" {
			return
		}
		deleteSession(value)
	case ActionClearSession:
		clearCurrentSession()
		setMode("chat")
	case ActionSetModel:
		_, value := splitIdentifier(identifier)
		setModel(value)
		setMode("chat")
	case ActionSetCliMode:
		_, value := splitIdentifier(identifier)
		setCliMode(value)
		setMode("chat")
	case ActionModeChat:
		setMode("chat")
	case ActionModeModels:
		setMode("models")
	case ActionModeSessions:
		setMode("sessions")
	case ActionTogglePersist:
		togglePersist(identifier)
		setMode("sessions")
	case ActionTogglePin:
		togglePin(identifier)
		setMode("sessions")
	case ActionRenameSession:
		renameSession(identifier, strings.TrimSpace(query))
		setMode("sessions")
	default:
		logger.WithField("action", action).Warn("unknown action")
	}
}

func Icon() string {
	return config.Icon
}

func HideFromProviderlist() bool {
	return config.HideFromProviderlist
}

func State(_ string) *pb.ProviderStateResponse {
	states := []string{}
	if mode := effectiveCliMode(); mode != "" {
		states = append(states, "cli:"+mode)
	}
	if model := currentModel(); model != "" {
		states = append(states, "model:"+model)
	}
	if session := currentSession(); session != nil {
		states = append(states, "session:"+session.Name)
	}
	states = append(states, "mode:"+currentMode())
	return &pb.ProviderStateResponse{States: states}
}

func buildAskEntry(query string) *pb.QueryResponse_Item {
	return &pb.QueryResponse_Item{
		Identifier: "ask",
		Text:       fmt.Sprintf("%s %s", strings.TrimSpace(config.AskPrefix), query),
		Subtext:    sessionContextLabel(),
		Icon:       config.Icon,
		Provider:   Name,
		Score:      1_000_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionAsk},
	}
}

func buildChatEntries(query string, exact bool) []*pb.QueryResponse_Item {
	entries := []*pb.QueryResponse_Item{}
	session := currentSession()
	transcriptPath := ""
	if session != nil {
		transcriptPath = ensureTranscript(session, "", "")
	}

	if query != "" {
		entries = append(entries, &pb.QueryResponse_Item{
			Identifier:  "send",
			Text:        fmt.Sprintf("Send: %s", query),
			Subtext:     sessionContextLabel(),
			Icon:        config.Icon,
			Provider:    Name,
			Score:       1_000_000_000,
			Type:        pb.QueryResponse_REGULAR,
			Actions:     []string{ActionSend, ActionModeModels, ActionModeSessions, ActionTogglePersist, ActionCopyAnswer, ActionCopyTranscript, ActionEditTranscript, ActionApplyTranscript},
			Preview:     transcriptPath,
			PreviewType: "file",
		})
	}

	chatEntry := &pb.QueryResponse_Item{
		Identifier:  "chat",
		Text:        "Chat",
		Subtext:     sessionContextLabel(),
		Icon:        config.Icon,
		Provider:    Name,
		Score:       950_000_000,
		Type:        pb.QueryResponse_REGULAR,
		Actions:     []string{ActionModeModels, ActionModeSessions, ActionModeChat, ActionTogglePersist, ActionClearSession, ActionCopyAnswer, ActionCopyTranscript, ActionEditTranscript, ActionApplyTranscript},
		Preview:     transcriptPath,
		PreviewType: "file",
	}
	entries = append(entries, chatEntry)

	if session != nil {
		entries = append(entries, buildErrorEntry(session, query, exact)...)
		entries = append(entries, buildCommandEntries(session, query, exact)...)
	}

	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "mode_models",
		Text:       "Models",
		Subtext:    "Select a model",
		Icon:       "view-refresh",
		Provider:   Name,
		Score:      200_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionModeModels},
	})
	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "mode_sessions",
		Text:       "Sessions",
		Subtext:    "Manage chat sessions",
		Icon:       "user-available",
		Provider:   Name,
		Score:      199_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionModeSessions},
	})

	return entries
}

func buildModelsMenu(_ string, _ bool) []*pb.QueryResponse_Item {
	entries := []*pb.QueryResponse_Item{}

	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "mode_chat",
		Text:       "Back to chat",
		Subtext:    "Return to conversation",
		Icon:       "go-previous",
		Provider:   Name,
		Score:      1_000_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionModeChat},
	})

	entries = append(entries, buildModelEntries("", false)...)
	entries = append(entries, buildCliEntries("", false)...)

	return entries
}

func buildSessionsMenu(_ string, _ bool) []*pb.QueryResponse_Item {
	entries := []*pb.QueryResponse_Item{}

	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "mode_chat",
		Text:       "Back to chat",
		Subtext:    "Return to conversation",
		Icon:       "go-previous",
		Provider:   Name,
		Score:      1_000_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionModeChat},
	})

	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "new_temp_session",
		Text:       "New temporary session",
		Subtext:    "Default (not saved)",
		Icon:       "document-new",
		Provider:   Name,
		Score:      900_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionNewTemp, ActionModeChat},
	})
	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "new_session",
		Text:       "New persistent session",
		Subtext:    "Saved to disk",
		Icon:       "document-new",
		Provider:   Name,
		Score:      899_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionNewSession, ActionModeChat},
	})
	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "persist_current",
		Text:       "Persist current session",
		Subtext:    "Save the active session to disk",
		Icon:       "document-save",
		Provider:   Name,
		Score:      898_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionTogglePersist, ActionModeChat},
	})

	sessionsList := listSessions()
	sort.SliceStable(sessionsList, func(i, j int) bool {
		if sessionsList[i].Pinned != sessionsList[j].Pinned {
			return sessionsList[i].Pinned
		}
		return sessionsList[i].UpdatedAt.After(sessionsList[j].UpdatedAt)
	})

	score := int32(700_000_000)
	for _, session := range sessionsList {
		entry := &pb.QueryResponse_Item{
			Identifier: fmt.Sprintf("session%s%s", identifierSep, session.ID),
			Text:       session.Name,
			Subtext:    sessionSummary(session),
			Icon:       "user-available",
			Provider:   Name,
			Score:      score,
			Type:       pb.QueryResponse_REGULAR,
			Actions:    []string{ActionSelectSession, ActionRenameSession, ActionTogglePin, ActionDeleteSession, ActionTogglePersist, ActionModeChat},
		}
		score--

		if session.Pinned {
			entry.State = append(entry.State, "pinned")
		}
		if isCurrentSession(session) {
			entry.State = append(entry.State, "current")
		}
		if session.Persistent {
			entry.State = append(entry.State, "saved")
		} else {
			entry.State = append(entry.State, "temp")
		}

		entries = append(entries, entry)
	}

	return entries
}

func buildSessionEntries(query string, exact bool) []*pb.QueryResponse_Item {
	entries := []*pb.QueryResponse_Item{}

	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "new_session",
		Text:       "New persistent session",
		Subtext:    "Saved to disk",
		Icon:       "document-new",
		Provider:   Name,
		Score:      900_000_000,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionNewSession},
	})

	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "new_temp_session",
		Text:       "New temporary session",
		Subtext:    "Not saved to disk",
		Icon:       "document-new",
		Provider:   Name,
		Score:      900_000_000 - 1,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionNewTemp},
	})

	entries = append(entries, &pb.QueryResponse_Item{
		Identifier: "clear_session",
		Text:       "Clear current session",
		Subtext:    "Remove stored messages for current session",
		Icon:       "edit-clear",
		Provider:   Name,
		Score:      900_000_000 - 2,
		Type:       pb.QueryResponse_REGULAR,
		Actions:    []string{ActionClearSession},
	})

	for _, session := range listSessions() {
		entry := &pb.QueryResponse_Item{
			Identifier: fmt.Sprintf("session%s%s", identifierSep, session.ID),
			Text:       session.Name,
			Subtext:    sessionSummary(session),
			Icon:       "user-available",
			Provider:   Name,
			Type:       pb.QueryResponse_REGULAR,
			Actions:    []string{ActionSelectSession, ActionDeleteSession},
		}

		if isCurrentSession(session) {
			entry.State = []string{"current"}
			entry.Score = 800_000_000
		}

		if matchAndScore(entry, query, exact) {
			entries = append(entries, entry)
		}
	}

	if matchAny(entries, query) {
		return filterEntries(entries, query, exact)
	}

	return entries
}

func buildModelEntries(query string, exact bool) []*pb.QueryResponse_Item {
	entries := []*pb.QueryResponse_Item{}
	for i, model := range config.Models {
		entry := &pb.QueryResponse_Item{
			Identifier: fmt.Sprintf("model%s%s", identifierSep, model),
			Text:       model,
			Subtext:    "Model",
			Icon:       "view-refresh",
			Provider:   Name,
			Score:      int32(700_000_000 - i),
			Type:       pb.QueryResponse_REGULAR,
			Actions:    []string{ActionSetModel, ActionModeChat},
		}
		if currentModel() == model {
			entry.State = []string{"current"}
		}
		if matchAndScore(entry, query, exact) {
			entries = append(entries, entry)
		}
	}
	return entries
}

func buildCliEntries(query string, exact bool) []*pb.QueryResponse_Item {
	if strings.ToLower(strings.TrimSpace(config.CliMode)) != "both" {
		return nil
	}

	entries := []*pb.QueryResponse_Item{}
	cliOptions := []string{"copilot", "gh"}
	for i, mode := range cliOptions {
		entry := &pb.QueryResponse_Item{
			Identifier: fmt.Sprintf("cli%s%s", identifierSep, mode),
			Text:       fmt.Sprintf("CLI: %s", mode),
			Subtext:    "Select Copilot CLI backend",
			Icon:       "utilities-terminal",
			Provider:   Name,
			Score:      int32(650_000_000 - i),
			Type:       pb.QueryResponse_REGULAR,
			Actions:    []string{ActionSetCliMode, ActionModeChat},
		}
		if effectiveCliMode() == mode {
			entry.State = []string{"current"}
		}
		if matchAndScore(entry, query, exact) {
			entries = append(entries, entry)
		}
	}
	return entries
}

func buildAnswerEntry(session *Session, query string, exact bool) []*pb.QueryResponse_Item {
	if session.LastAnswer == "" {
		return nil
	}

	summary := strings.TrimSpace(session.LastAnswer)
	if len([]rune(summary)) > 160 {
		summary = string([]rune(summary)[:160]) + "…"
	}

	entry := &pb.QueryResponse_Item{
		Identifier:  "answer",
		Text:        "Last answer",
		Subtext:     summary,
		Icon:        "dialog-information",
		Provider:    Name,
		Type:        pb.QueryResponse_REGULAR,
		Actions:     []string{ActionCopyAnswer},
		Preview:     session.LastAnswer,
		PreviewType: "text",
	}

	if query != "" {
		entry.Score = 900_000_000
		return []*pb.QueryResponse_Item{entry}
	}

	if matchAndScore(entry, query, exact) {
		return []*pb.QueryResponse_Item{entry}
	}
	return nil
}

func buildErrorEntry(session *Session, query string, exact bool) []*pb.QueryResponse_Item {
	if session.LastError == "" {
		return nil
	}

	summary := strings.TrimSpace(session.LastError)
	if len([]rune(summary)) > 160 {
		summary = string([]rune(summary)[:160]) + "…"
	}

	entry := &pb.QueryResponse_Item{
		Identifier:  "error",
		Text:        "Last error",
		Subtext:     summary,
		Icon:        "dialog-warning",
		Provider:    Name,
		Type:        pb.QueryResponse_REGULAR,
		Actions:     []string{ActionCopyError},
		Preview:     session.LastError,
		PreviewType: "text",
	}

	if query != "" {
		entry.Score = 950_000_000
		return []*pb.QueryResponse_Item{entry}
	}

	if matchAndScore(entry, query, exact) {
		return []*pb.QueryResponse_Item{entry}
	}
	return nil
}

func buildCommandEntries(session *Session, query string, exact bool) []*pb.QueryResponse_Item {
	entries := []*pb.QueryResponse_Item{}
	if session != nil && len(session.LastCommands) > 0 {
		allCommands := strings.Join(session.LastCommands, "\n")
		entry := &pb.QueryResponse_Item{
			Identifier:  "cmd_all",
			Text:        "Copy all commands",
			Subtext:     fmt.Sprintf("%d commands", len(session.LastCommands)),
			Icon:        "edit-copy",
			Provider:    Name,
			Type:        pb.QueryResponse_REGULAR,
			Actions:     []string{ActionCopyAll},
			Preview:     allCommands,
			PreviewType: "text",
		}
		if query != "" {
			entry.Score = 850_000_000
			entries = append(entries, entry)
		} else if matchAndScore(entry, query, exact) {
			entries = append(entries, entry)
		}
	}
	for i, cmd := range session.LastCommands {
		display := cmd
		if len([]rune(display)) > 120 {
			display = string([]rune(display)[:120]) + "…"
		}
		entry := &pb.QueryResponse_Item{
			Identifier:  fmt.Sprintf("cmd%s%d", identifierSep, i),
			Text:        display,
			Subtext:     "Command from last answer",
			Icon:        "utilities-terminal",
			Provider:    Name,
			Type:        pb.QueryResponse_REGULAR,
			Actions:     []string{ActionOpenTerminal, ActionCopyCommand},
			Preview:     cmd,
			PreviewType: "text",
		}
		if query != "" {
			entry.Score = int32(800_000_000 - i)
			entries = append(entries, entry)
		} else if matchAndScore(entry, query, exact) {
			entries = append(entries, entry)
		}
	}
	return entries
}

func matchAndScore(entry *pb.QueryResponse_Item, query string, exact bool) bool {
	if query == "" {
		if entry.Score == 0 {
			entry.Score = 100_000
		}
		return true
	}

	score, positions, start := common.FuzzyScore(query, entry.Text, exact)
	entry.Score = score
	entry.Fuzzyinfo = &pb.QueryResponse_Item_FuzzyInfo{
		Start:     start,
		Field:     "text",
		Positions: positions,
	}
	return entry.Score > config.MinScore
}

func matchAny(entries []*pb.QueryResponse_Item, query string) bool {
	if query == "" {
		return true
	}
	for _, entry := range entries {
		if entry.Score > config.MinScore {
			return true
		}
	}
	return false
}

func filterEntries(entries []*pb.QueryResponse_Item, query string, exact bool) []*pb.QueryResponse_Item {
	if query == "" {
		return entries
	}
	filtered := []*pb.QueryResponse_Item{}
	for _, entry := range entries {
		if entry.Score == 0 {
			matchAndScore(entry, query, exact)
		}
		if entry.Score > config.MinScore {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func askCopilot(question string) error {
	session := currentSession()
	if session == nil {
		return errbuilder.New().WithCode(errbuilder.CodeFailedPrecondition).WithMsg("no session available")
	}

	return runCopilotStream(session, question)
}

func runCopilotStream(session *Session, question string) error {
	prompt := buildPrompt(session, question)
	cmd, err := buildCopilotCommand(prompt)
	if err != nil {
		session.LastError = err.Error()
		session.UpdatedAt = time.Now()
		saveSession(session)
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	transcriptPath := ensureTranscript(session, question, "")

	var outBuf strings.Builder
	var errBuf strings.Builder
	done := make(chan struct{})

	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, readErr := stdout.Read(buf)
			if n > 0 {
				outBuf.Write(buf[:n])
				partial := strings.TrimSpace(outBuf.String())
				session.LastAnswer = partial
				writeTranscriptFile(transcriptPath, renderTranscript(session, question, partial, ""))
			}
			if readErr != nil {
				break
			}
		}
	}()

	go func() {
		buf := make([]byte, 2048)
		for {
			n, readErr := stderr.Read(buf)
			if n > 0 {
				errBuf.Write(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	<-done

	final := strings.TrimSpace(outBuf.String())
	if waitErr != nil || final == "" {
		if final == "" {
			waitErr = fmt.Errorf("empty response")
		}
		errMsg := strings.TrimSpace(errBuf.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		session.LastError = errMsg
		session.UpdatedAt = time.Now()
		saveSession(session)
		writeTranscriptFile(transcriptPath, renderTranscript(session, question, "", errMsg))
		return errbuilder.New().
			WithCode(errbuilder.CodeUnavailable).
			WithMsg("copilot CLI invocation failed").
			WithCause(fmt.Errorf("cmd=%s err=%w out=%s", cmd.String(), waitErr, errMsg))
	}

	now := time.Now()
	session.Messages = append(session.Messages, Message{Role: "user", Content: question, Time: now})
	session.Messages = append(session.Messages, Message{Role: "assistant", Content: final, Time: time.Now()})
	session.LastAnswer = final
	session.LastCommands = extractCommands(final)
	session.LastError = ""
	session.UpdatedAt = time.Now()

	pruneMessages(session)
	saveSession(session)
	writeTranscriptFile(transcriptPath, renderTranscript(session, "", "", ""))

	return nil
}

func buildPrompt(session *Session, question string) string {
	var b strings.Builder
	if config.SystemPrompt != "" {
		b.WriteString(config.SystemPrompt)
		b.WriteString("\n\n")
	}

	history := session.Messages
	if config.MaxContextMessages > 0 && len(history) > config.MaxContextMessages {
		history = history[len(history)-config.MaxContextMessages:]
	}

	if len(history) > 0 {
		b.WriteString("Conversation so far:\n")
		for _, msg := range history {
			role := strings.Title(msg.Role)
			b.WriteString(fmt.Sprintf("%s: %s\n", role, msg.Content))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("User: %s\nAssistant:", question))
	return b.String()
}

func executeCopilot(prompt string) (string, error) {
	cmd, err := buildCopilotCommand(prompt)
	if err != nil {
		return "", err
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", errbuilder.New().
			WithCode(errbuilder.CodeUnavailable).
			WithMsg("copilot CLI invocation failed").
			WithCause(fmt.Errorf("cmd=%s err=%w out=%s", cmd.String(), err, strings.TrimSpace(string(output))))
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return "", errbuilder.New().
			WithCode(errbuilder.CodeUnavailable).
			WithMsg("copilot CLI returned empty response").
			WithCause(errors.New("empty response"))
	}

	return trimmed, nil
}

func buildCopilotCommand(prompt string) (*exec.Cmd, error) {
	mode := effectiveCliMode()
	model := currentModel()

	switch mode {
	case "copilot":
		args := append([]string{}, config.CopilotArgs...)
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, "-p", prompt)
		return exec.Command("copilot", args...), nil
	case "gh":
		args := []string{"copilot", "--"}
		args = append(args, config.GhCopilotArgs...)
		if model != "" {
			args = append(args, "--model", model)
		}
		args = append(args, "-p", prompt)
		return exec.Command("gh", args...), nil
	default:
		return nil, errbuilder.New().WithCode(errbuilder.CodeFailedPrecondition).WithMsg("no available copilot CLI")
	}
}

func extractCommands(answer string) []string {
	if commandRegex != nil && commandRegexErr == nil {
		return regexCommands(answer)
	}

	commands := []string{}
	seen := map[string]struct{}{}

	add := func(cmd string) {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			return
		}
		if _, ok := seen[cmd]; ok {
			return
		}
		seen[cmd] = struct{}{}
		commands = append(commands, cmd)
	}

	for _, block := range extractCodeBlocks(answer) {
		content := block.content
		isShell := isShellLang(block.lang)
		scanner := bufio.NewScanner(strings.NewReader(content))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "$ ") {
				add(strings.TrimSpace(strings.TrimPrefix(line, "$ ")))
				continue
			}
			if strings.HasPrefix(line, "> ") {
				add(strings.TrimSpace(strings.TrimPrefix(line, "> ")))
				continue
			}
			if isShell {
				add(line)
			}
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(answer))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "$ ") {
			add(strings.TrimSpace(strings.TrimPrefix(line, "$ ")))
		}
		if strings.HasPrefix(line, "> ") {
			add(strings.TrimSpace(strings.TrimPrefix(line, "> ")))
		}
	}

	return commands
}

func regexCommands(answer string) []string {
	matches := commandRegex.FindAllStringSubmatch(answer, -1)
	if len(matches) == 0 {
		return nil
	}

	commands := []string{}
	seen := map[string]struct{}{}
	for _, match := range matches {
		var cmd string
		if len(match) > 1 {
			cmd = match[1]
		} else {
			cmd = match[0]
		}
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		if _, ok := seen[cmd]; ok {
			continue
		}
		seen[cmd] = struct{}{}
		commands = append(commands, cmd)
	}
	return commands
}

type codeBlock struct {
	lang    string
	content string
}

func extractCodeBlocks(answer string) []codeBlock {
	re := regexp.MustCompile("(?s)```([a-zA-Z0-9_-]+)?\\n(.*?)```")
	matches := re.FindAllStringSubmatch(answer, -1)
	blocks := []codeBlock{}
	for _, match := range matches {
		lang := strings.TrimSpace(match[1])
		content := strings.TrimSpace(match[2])
		if content == "" {
			continue
		}
		blocks = append(blocks, codeBlock{
			lang:    strings.ToLower(lang),
			content: content,
		})
	}
	return blocks
}

func isShellLang(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "sh", "bash", "zsh", "shell":
		return true
	default:
		return false
	}
}

func copyToClipboard(content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	cmd := exec.Command("sh", "-c", config.ClipboardCommand)
	cmd.Stdin = strings.NewReader(content)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errbuilder.New().
			WithCode(errbuilder.CodeInternal).
			WithMsg("clipboard command failed").
			WithCause(fmt.Errorf("err=%w out=%s", err, strings.TrimSpace(string(output))))
	}
	return nil
}

func copyTranscript() error {
	session := currentSession()
	if session == nil {
		return nil
	}
	path := ensureTranscript(session, "", "")
	if path == "" {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return copyToClipboard(string(content))
}

func editTranscript() error {
	session := currentSession()
	if session == nil {
		return nil
	}
	path := ensureTranscript(session, "", "")
	if path == "" {
		return nil
	}

	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "xdg-open"
	}

	cmd := exec.Command("sh", "-c", fmt.Sprintf("%s %s", editor, shellescape.Quote(path)))
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

func applyTranscriptEdits() error {
	session := currentSession()
	if session == nil {
		return nil
	}
	path := ensureTranscript(session, "", "")
	if path == "" {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	messages := parseTranscript(string(content))
	if len(messages) == 0 {
		return nil
	}

	session.Messages = messages
	session.LastError = ""
	session.LastAnswer = lastAssistantMessage(messages)
	session.LastCommands = extractCommands(session.LastAnswer)
	session.UpdatedAt = time.Now()

	pruneMessages(session)
	saveSession(session)
	ensureTranscript(session, "", "")
	return nil
}

func openTerminalPrefill(command string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}

	prefill := strings.ReplaceAll(config.TerminalPrefillCmd, "%CMD%", shellescape.Quote(command))
	run := strings.TrimSpace(fmt.Sprintf("%s %s", common.LaunchPrefix(""), common.WrapWithTerminal(prefill)))
	cmd := exec.Command("sh", "-c", run)
	if err := cmd.Start(); err != nil {
		return errbuilder.New().
			WithCode(errbuilder.CodeInternal).
			WithMsg("failed to open terminal").
			WithCause(err)
	}
	return nil
}

func commandByIdentifier(identifier string) string {
	kind, value := splitIdentifier(identifier)
	if kind != "cmd" {
		return ""
	}
	index, err := strconv.Atoi(value)
	if err != nil {
		return ""
	}

	session := currentSession()
	if session == nil {
		return ""
	}
	if index < 0 || index >= len(session.LastCommands) {
		return ""
	}
	return session.LastCommands[index]
}

func splitIdentifier(identifier string) (string, string) {
	parts := strings.SplitN(identifier, identifierSep, 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return identifier, ""
}

func currentSession() *Session {
	mu.Lock()
	defer mu.Unlock()

	if state != nil && state.CurrentSessionPersistent {
		if session, ok := sessions[state.CurrentSessionID]; ok {
			return session
		}
	}

	if ephemeral != nil {
		return ephemeral
	}

	ephemeral = newSessionLocked(false)
	return ephemeral
}

func setCurrentSession(session *Session) {
	if session == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()

	if session.Persistent {
		state.CurrentSessionID = session.ID
		state.CurrentSessionPersistent = true
	} else {
		ephemeral = session
		state.CurrentSessionID = session.ID
		state.CurrentSessionPersistent = false
	}
	saveState()
}

func selectSession(id string) {
	mu.Lock()
	defer mu.Unlock()

	if id == "ephemeral" && ephemeral != nil {
		state.CurrentSessionID = ephemeral.ID
		state.CurrentSessionPersistent = false
		saveState()
		return
	}

	if _, ok := sessions[id]; ok {
		state.CurrentSessionID = id
		state.CurrentSessionPersistent = true
		saveState()
		return
	}
}

func clearCurrentSession() {
	session := currentSession()
	if session == nil {
		return
	}

	session.Messages = nil
	session.LastAnswer = ""
	session.LastCommands = nil
	session.LastError = ""
	session.UpdatedAt = time.Now()
	pruneMessages(session)
	saveSession(session)
	ensureTranscript(session, "", "")
}

func deleteSession(id string) {
	mu.Lock()
	defer mu.Unlock()

	session, ok := sessions[id]
	if !ok {
		return
	}
	if session.Persistent {
		path := sessionPath(id)
		_ = os.Remove(path)
	}
	_ = os.Remove(sessionTranscriptPath(session))
	delete(sessions, id)

	if state.CurrentSessionID == id {
		state.CurrentSessionID = ""
		state.CurrentSessionPersistent = false
	}
	saveState()
}

func newSession(persistent bool) *Session {
	mu.Lock()
	defer mu.Unlock()
	return newSessionLocked(persistent)
}

func newSessionLocked(persistent bool) *Session {
	now := time.Now()
	session := &Session{
		ID:         uuid.NewString(),
		Name:       fmt.Sprintf("Session %s", now.Format("2006-01-02 15:04")),
		Persistent: persistent,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if persistent {
		sessions[session.ID] = session
		saveSession(session)
	}
	return session
}

func listSessions() []*Session {
	mu.Lock()
	defer mu.Unlock()

	result := []*Session{}
	for _, session := range sessions {
		result = append(result, session)
	}
	if ephemeral != nil {
		result = append(result, ephemeral)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result
}

func isCurrentSession(session *Session) bool {
	if session == nil || state == nil {
		return false
	}
	if session.Persistent && state.CurrentSessionPersistent {
		return state.CurrentSessionID == session.ID
	}
	if !session.Persistent && !state.CurrentSessionPersistent && ephemeral != nil {
		return session.ID == ephemeral.ID
	}
	return false
}

func sessionSummary(session *Session) string {
	if session == nil {
		return ""
	}
	kind := "persistent"
	if !session.Persistent {
		kind = "temporary"
	}
	return fmt.Sprintf("%s • updated %s", kind, session.UpdatedAt.Format(time.RFC822))
}

func sessionContextLabel() string {
	session := currentSession()
	if session == nil {
		return ""
	}
	model := currentModel()
	mode := effectiveCliMode()
	return fmt.Sprintf("%s | model: %s | cli: %s", session.Name, model, mode)
}

func togglePersist(identifier string) {
	kind, value := splitIdentifier(identifier)
	var session *Session
	if kind == "session" {
		session = sessions[value]
	} else {
		session = currentSession()
	}
	if session == nil || session.Persistent {
		return
	}
	session.Persistent = true
	sessions[session.ID] = session
	state.CurrentSessionID = session.ID
	state.CurrentSessionPersistent = true
	saveSession(session)
	saveState()
}

func togglePin(identifier string) {
	kind, value := splitIdentifier(identifier)
	var session *Session
	if kind == "session" {
		session = sessions[value]
	} else {
		session = currentSession()
	}
	if session == nil {
		return
	}
	session.Pinned = !session.Pinned
	session.UpdatedAt = time.Now()
	saveSession(session)
}

func renameSession(identifier, newName string) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return
	}
	kind, value := splitIdentifier(identifier)
	var session *Session
	if kind == "session" {
		session = sessions[value]
	} else {
		session = currentSession()
	}
	if session == nil {
		return
	}
	session.Name = newName
	session.UpdatedAt = time.Now()
	saveSession(session)
}

func saveSession(session *Session) {
	if session == nil || !session.Persistent {
		return
	}
	path := sessionPath(session.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.WithError(err).Warn("failed to create session dir")
		return
	}
	b, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		logger.WithError(err).Warn("failed to marshal session")
		return
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		logger.WithError(err).Warn("failed to write session file")
		return
	}
	pruneSessionsLocked()
}

func loadSessions() {
	mu.Lock()
	defer mu.Unlock()

	dir := sessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			logger.WithError(err).Warn("failed to read session")
			continue
		}
		var session Session
		if err := json.Unmarshal(content, &session); err != nil {
			logger.WithError(err).Warn("failed to parse session")
			continue
		}
		if session.ID == "" {
			continue
		}
		session.Persistent = true
		sessions[session.ID] = &session
	}
}

func loadState() {
	path := statePath()
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var loaded CopilotState
	if err := json.Unmarshal(content, &loaded); err != nil {
		logger.WithError(err).Warn("failed to parse state")
		return
	}
	if loaded.CurrentModel != "" {
		state.CurrentModel = loaded.CurrentModel
	}
	if loaded.CurrentCliMode != "" {
		state.CurrentCliMode = loaded.CurrentCliMode
	}
	if loaded.CurrentMode != "" {
		state.CurrentMode = loaded.CurrentMode
	}
	state.CurrentSessionID = loaded.CurrentSessionID
	state.CurrentSessionPersistent = loaded.CurrentSessionPersistent
}

func saveState() {
	if state == nil {
		return
	}
	toSave := *state
	if !toSave.CurrentSessionPersistent {
		toSave.CurrentSessionID = ""
	}
	path := statePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.WithError(err).Warn("failed to create state dir")
		return
	}
	b, err := json.MarshalIndent(toSave, "", "  ")
	if err != nil {
		logger.WithError(err).Warn("failed to marshal state")
		return
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		logger.WithError(err).Warn("failed to write state")
	}
}

func ensureDefaultModel() {
	if state.CurrentModel == "" {
		state.CurrentModel = config.DefaultModel
	}
	found := false
	for _, model := range config.Models {
		if model == state.CurrentModel {
			found = true
			break
		}
	}
	if !found && config.DefaultModel != "" {
		config.Models = append([]string{config.DefaultModel}, config.Models...)
	}
	saveState()
}

func ensureDefaultMode() {
	if state.CurrentMode == "" {
		state.CurrentMode = "chat"
		saveState()
	}
}

func setModel(model string) {
	if model == "" {
		return
	}
	state.CurrentModel = model
	saveState()
}

func setCliMode(mode string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "copilot" && mode != "gh" {
		return
	}
	state.CurrentCliMode = mode
	saveState()
}

func setMode(mode string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "chat", "models", "sessions":
		if state.CurrentMode != mode {
			state.CurrentMode = mode
			lastModeChange = time.Now()
			saveState()
		}
	}
}

func currentModel() string {
	if state != nil && state.CurrentModel != "" {
		return state.CurrentModel
	}
	return config.DefaultModel
}

func currentMode() string {
	if state != nil && state.CurrentMode != "" {
		return state.CurrentMode
	}
	return "chat"
}

func effectiveCliMode() string {
	cfgMode := strings.ToLower(strings.TrimSpace(config.CliMode))
	switch cfgMode {
	case "copilot":
		if hasCopilot() {
			return "copilot"
		}
		return ""
	case "gh":
		if hasGh() {
			return "gh"
		}
		return ""
	case "both":
		if state != nil && (state.CurrentCliMode == "copilot" || state.CurrentCliMode == "gh") {
			if state.CurrentCliMode == "copilot" && hasCopilot() {
				return "copilot"
			}
			if state.CurrentCliMode == "gh" && hasGh() {
				return "gh"
			}
		}
		if hasCopilot() {
			return "copilot"
		}
		if hasGh() {
			return "gh"
		}
		return ""
	case "auto", "":
		if hasCopilot() {
			return "copilot"
		}
		if hasGh() {
			return "gh"
		}
		return ""
	default:
		return ""
	}
}

func hasCopilot() bool {
	_, err := exec.LookPath("copilot")
	return err == nil
}

func hasGh() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

func sessionPath(id string) string {
	return filepath.Join(sessionsDir(), fmt.Sprintf("%s.json", id))
}

func sessionsDir() string {
	return filepath.Join(config.SessionDir, "sessions")
}

func statePath() string {
	return filepath.Join(config.SessionDir, "state.json")
}

func transcriptsDir() string {
	return filepath.Join(config.SessionDir, "transcripts")
}

func sessionTranscriptPath(session *Session) string {
	if session == nil {
		return ""
	}
	return filepath.Join(transcriptsDir(), fmt.Sprintf("%s.txt", session.ID))
}

func ensureTranscript(session *Session, pendingQuestion, pendingAnswer string) string {
	path := sessionTranscriptPath(session)
	if path == "" {
		return ""
	}
	content := renderTranscript(session, pendingQuestion, pendingAnswer, session.LastError)
	writeTranscriptFile(path, content)
	return path
}

func writeTranscriptFile(path, content string) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger.WithError(err).Warn("failed to create transcript dir")
		return
	}
	if strings.TrimSpace(content) == "" {
		content = "No messages yet."
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		logger.WithError(err).Warn("failed to write transcript")
	}
}

func renderTranscript(session *Session, pendingQuestion, pendingAnswer, pendingError string) string {
	var b strings.Builder
	if session == nil {
		return ""
	}

	for _, msg := range session.Messages {
		role := strings.Title(msg.Role)
		b.WriteString(fmt.Sprintf("%s: %s\n\n", role, msg.Content))
	}

	if pendingQuestion != "" {
		b.WriteString(fmt.Sprintf("User: %s\n\n", pendingQuestion))
	}
	if pendingAnswer != "" {
		b.WriteString(fmt.Sprintf("Assistant: %s\n\n", pendingAnswer))
	}
	if pendingError != "" {
		b.WriteString(fmt.Sprintf("Error: %s\n", pendingError))
	}

	return strings.TrimSpace(b.String())
}

func parseTranscript(content string) []Message {
	lines := strings.Split(content, "\n")
	var messages []Message

	var currentRole string
	var buffer []string
	flush := func() {
		if currentRole == "" {
			buffer = nil
			return
		}
		text := strings.TrimSpace(strings.Join(buffer, "\n"))
		if text == "" {
			buffer = nil
			return
		}
		messages = append(messages, Message{
			Role:    strings.ToLower(currentRole),
			Content: text,
			Time:    time.Now(),
		})
		buffer = nil
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "User:"):
			flush()
			currentRole = "user"
			buffer = append(buffer, strings.TrimSpace(strings.TrimPrefix(line, "User:")))
		case strings.HasPrefix(line, "Assistant:"):
			flush()
			currentRole = "assistant"
			buffer = append(buffer, strings.TrimSpace(strings.TrimPrefix(line, "Assistant:")))
		case strings.HasPrefix(line, "System:"):
			flush()
			currentRole = "system"
			buffer = append(buffer, strings.TrimSpace(strings.TrimPrefix(line, "System:")))
		case strings.HasPrefix(line, "Error:"):
			flush()
			currentRole = "error"
			buffer = append(buffer, strings.TrimSpace(strings.TrimPrefix(line, "Error:")))
		default:
			buffer = append(buffer, line)
		}
	}
	flush()

	filtered := []Message{}
	for _, msg := range messages {
		if msg.Role == "error" {
			continue
		}
		filtered = append(filtered, msg)
	}

	return filtered
}

func lastAssistantMessage(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return messages[i].Content
		}
	}
	return ""
}

func defaultSessionDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "elephant", "copilot")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "elephant-copilot")
	}
	return filepath.Join(home, ".local", "state", "elephant", "copilot")
}

func pruneMessages(session *Session) {
	if session == nil || config.MaxStoredMessages <= 0 {
		return
	}
	if len(session.Messages) <= config.MaxStoredMessages {
		return
	}
	session.Messages = session.Messages[len(session.Messages)-config.MaxStoredMessages:]
}

func pruneSessionsLocked() {
	if config.MaxSessions <= 0 {
		return
	}
	if len(sessions) <= config.MaxSessions {
		return
	}

	type pair struct {
		id   string
		time time.Time
	}
	list := []pair{}
	for id, session := range sessions {
		list = append(list, pair{id: id, time: session.UpdatedAt})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].time.Before(list[j].time)
	})

	excess := len(list) - config.MaxSessions
	for i := 0; i < excess; i++ {
		id := list[i].id
		_ = os.Remove(sessionPath(id))
		delete(sessions, id)
	}
}

func applyViperOverrides() {
	v := viper.New()
	v.SetEnvPrefix("ELEPHANT_COPILOT")
	v.AutomaticEnv()

	if v.IsSet("MODEL") {
		state.CurrentModel = v.GetString("MODEL")
	}
	if v.IsSet("CLI_MODE") {
		state.CurrentCliMode = v.GetString("CLI_MODE")
	}
}

func matchEntry(entry *pb.QueryResponse_Item, query string) bool {
	if query == "" {
		return true
	}
	score, _, _ := common.FuzzyScore(query, entry.Text, false)
	return score > config.MinScore
}

func matchAnyEntries(entries []*pb.QueryResponse_Item, query string) bool {
	for _, entry := range entries {
		if matchEntry(entry, query) {
			return true
		}
	}
	return false
}

func errorFrom(err error, msg string) error {
	if err == nil {
		return nil
	}
	return errbuilder.New().
		WithCode(errbuilder.CodeInternal).
		WithMsg(msg).
		WithCause(err)
}

func must(err error) {
	if err == nil {
		return
	}
	panic(err)
}

func ensureErr(err error) error {
	if err == nil {
		return nil
	}
	var eb *errbuilder.ErrBuilder
	if errors.As(err, &eb) {
		return err
	}
	return errorFrom(err, "operation failed")
}
