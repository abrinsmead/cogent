package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
)

// Interactive runs an interactive REPL without a TUI framework.
// It reads from stdin, prints to stdout, and handles confirmations inline.
type Interactive struct {
	provider api.Provider
	cwd      string
	prompt   string // initial prompt (optional)

	ag          *agent.Agent
	persistID   string
	sessionName string
	nameSet     bool
	createdAt   time.Time
	totalCost   float64
	contextUsed int

	// stdinCh delivers lines read from stdin by a background goroutine.
	stdinCh chan string
}

func NewInteractive(provider api.Provider, cwd, prompt string) *Interactive {
	return &Interactive{
		provider:  provider,
		cwd:       cwd,
		prompt:    prompt,
		persistID: generatePersistID(),
		createdAt: time.Now(),
		stdinCh:   make(chan string),
	}
}

func (it *Interactive) Run() error {
	// Start a background goroutine to read lines from stdin.
	// This goroutine lives for the entire process lifetime.
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			it.stdinCh <- scanner.Text()
		}
		close(it.stdinCh)
	}()

	// confirmReqCh: agent goroutine sends a confirm request here.
	// confirmReplyCh: main goroutine sends the user's answer back.
	confirmReqCh := make(chan confirmRequest)
	confirmReplyCh := make(chan agent.ConfirmResult)

	it.ag = agent.New(it.provider, it.cwd,
		agent.WithTextCallback(func(text string) {
			fmt.Printf("\n%s\n", text)
		}),
		agent.WithToolCallback(func(name, summary string) {
			color := Green
			switch name {
			case "bash", "write", "edit":
				color = Red
			}
			fmt.Printf("%s %s%s%s %s%s\n", Dim, color, name, Reset, Dim, summary+Reset)
		}),
		agent.WithToolResultCallback(func(name, result string, isError bool) {
			if result == "" {
				return
			}
			const maxLines = 5
			lines := strings.Split(result, "\n")
			total := len(lines)
			truncated := total > maxLines
			if truncated {
				lines = lines[:maxLines]
			}
			color := Dim
			if isError {
				color = Red
			}
			for _, line := range lines {
				fmt.Printf("%s  %s%s\n", color, line, Reset)
			}
			if truncated {
				fmt.Printf("%s%s  ... (%d lines total)%s\n", Bold, Yellow, total, Reset)
			}
		}),
		agent.WithConfirmCallback(func(name string, input map[string]any) agent.ConfirmResult {
			confirmReqCh <- confirmRequest{name: name, input: input}
			return <-confirmReplyCh
		}),
		agent.WithUsageCallback(func(usage api.Usage) {
			it.contextUsed = usage.InputTokens
			it.totalCost += it.provider.CostForUsage(usage)
		}),
		agent.WithCompactionCallback(func() {
			fmt.Printf("%s  ⚡ context compacted%s\n", Dim, Reset)
		}),
	)

	// Show custom tools
	if names := it.ag.Registry().CustomToolNames(); len(names) > 0 {
		fmt.Printf("%s  tools %s%s\n", Dim, strings.Join(names, ", "), Reset)
	}

	// Print banner
	mode := it.ag.GetPermissionMode()
	fmt.Printf("%scogent%s %s(%s)%s\n", Bold, Reset, Dim, mode, Reset)

	// Handle initial prompt if provided
	if it.prompt != "" {
		if err := it.runPrompt(it.prompt, confirmReqCh, confirmReplyCh); err != nil {
			fmt.Printf("%sError: %s%s\n", Yellow, err, Reset)
		}
	}

	// Main REPL loop
	for {
		fmt.Printf("\n%s❯%s ", Bold+Cyan, Reset)
		input, ok := <-it.stdinCh
		if !ok {
			break
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if handled, quit := it.handleCommand(input); handled {
			if quit {
				return nil
			}
			continue
		}

		if err := it.runPrompt(input, confirmReqCh, confirmReplyCh); err != nil {
			fmt.Printf("%sError: %s%s\n", Yellow, err, Reset)
		}
	}

	it.save()
	return nil
}

type confirmRequest struct {
	name  string
	input map[string]any
}

func (it *Interactive) runPrompt(prompt string, confirmReqCh chan confirmRequest, confirmReplyCh chan agent.ConfirmResult) error {
	// Auto-name session from first prompt
	if !it.nameSet {
		name := strings.TrimSpace(prompt)
		if idx := strings.IndexByte(name, '\n'); idx > 0 {
			name = name[:idx]
		}
		if len(name) > 24 {
			name = name[:24] + "…"
		}
		if name != "" {
			it.sessionName = name
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ctrl+C cancels the current agent call, not the process.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- it.ag.SendCtx(ctx, prompt)
	}()

	for {
		select {
		case err := <-doneCh:
			it.save()
			if err != nil && ctx.Err() != nil {
				fmt.Printf("\n%s(interrupted)%s\n", Dim, Reset)
				return nil
			}
			return err

		case <-sigCh:
			cancel()

		case req := <-confirmReqCh:
			// Show the diff and prompt
			summary := SummarizeConfirm(req.name, req.input)
			diff := RenderDiff(req.name, req.input)
			if diff != "" {
				fmt.Println(diff)
			}
			fmt.Printf("%s%sAllow %s %s? [Y/n/a] %s", Bold, Yellow, req.name, summary, Reset)

			// Read the answer from stdin (or handle Ctrl+C during confirmation)
			select {
			case line, ok := <-it.stdinCh:
				if !ok {
					confirmReplyCh <- agent.ConfirmDeny
					cancel()
					continue
				}
				answer := strings.TrimSpace(strings.ToLower(line))
				switch answer {
				case "n", "no":
					fmt.Printf("%s  ✗ denied%s\n", Red, Reset)
					confirmReplyCh <- agent.ConfirmDeny
				case "a", "always":
					fmt.Printf("%s  ✓ always allow %s%s\n", Green, req.name, Reset)
					confirmReplyCh <- agent.ConfirmAlways
				default: // "", "y", "yes"
					fmt.Printf("%s  ✓ allowed%s\n", Green, Reset)
					confirmReplyCh <- agent.ConfirmAllow
				}
			case <-sigCh:
				fmt.Printf("\n%s  ✗ denied (interrupted)%s\n", Red, Reset)
				confirmReplyCh <- agent.ConfirmDeny
				cancel()
			}
		}
	}
}

func (it *Interactive) handleCommand(input string) (handled bool, quit bool) {
	switch {
	case input == "/quit" || input == "/exit" || input == "/q":
		it.save()
		return true, true

	case input == "/clear":
		it.ag.Reset()
		it.totalCost = 0
		it.contextUsed = 0
		fmt.Printf("%s(cleared)%s\n", Dim, Reset)
		return true, false

	case input == "/mode":
		cur := it.ag.GetPermissionMode()
		next := agent.CyclePermissionMode(cur)
		it.ag.SetPermissionMode(next)
		fmt.Printf("%s  mode → %s%s\n", Dim, next, Reset)
		return true, false

	case input == "/help":
		fmt.Printf("%sCommands: /help /clear /quit /mode /model /resume /cost%s\n", Dim, Reset)
		fmt.Printf("%s/mode cycles: Plan → Confirm → YOLO → Terminal%s\n", Dim, Reset)
		return true, false

	case input == "/cost":
		info := it.provider.Info()
		fmt.Printf("%sModel: %s/%s  Context: %d  Cost: $%.4f%s\n",
			Dim, info.ProviderID, info.Model, it.contextUsed, it.totalCost, Reset)
		return true, false

	case strings.HasPrefix(input, "/model"):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/model"))
		if arg == "" {
			info := it.provider.Info()
			fmt.Printf("%sCurrent model: %s/%s%s\n", Dim, info.ProviderID, info.Model, Reset)
			fmt.Printf("%sUsage: /model <provider/model> (e.g. /model openai/gpt-4o)%s\n", Dim, Reset)
			return true, false
		}
		spec := api.ParseModelSpec(arg)
		p, err := api.NewProvider(spec)
		if err != nil {
			fmt.Printf("%sError: %s%s\n", Yellow, err, Reset)
			return true, false
		}
		it.provider = p
		it.ag.SetProvider(p)
		it.contextUsed = 0
		fmt.Printf("%s  model → %s/%s%s\n", Dim, spec.Provider, spec.Model, Reset)
		return true, false

	case input == "/resume":
		it.handleResume("")
		return true, false

	case strings.HasPrefix(input, "/resume "):
		arg := strings.TrimSpace(strings.TrimPrefix(input, "/resume "))
		it.handleResume(arg)
		return true, false

	case strings.HasPrefix(input, "/"):
		fmt.Printf("%sUnknown command: %s (try /help)%s\n", Yellow, input, Reset)
		return true, false

	default:
		return false, false
	}
}

func (it *Interactive) handleResume(arg string) {
	sessions := listSavedSessions(it.cwd)
	if len(sessions) == 0 {
		fmt.Printf("%sNo saved sessions found.%s\n", Dim, Reset)
		return
	}

	if arg == "" {
		fmt.Printf("%sSaved sessions:%s\n", Dim, Reset)
		for i, sd := range sessions {
			age := time.Since(sd.UpdatedAt).Truncate(time.Minute)
			preview := sessionPreview(sd)
			fmt.Printf("%s  %d: %s (%s) %s%s\n", Dim, i+1, sd.Name, age, preview, Reset)
		}
		fmt.Printf("%sUse /resume <number> or /resume <name> to restore.%s\n", Dim, Reset)
		return
	}

	var sd *sessionData
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 && n <= len(sessions) {
		sd = &sessions[n-1]
	} else {
		lower := strings.ToLower(arg)
		for i := range sessions {
			if strings.Contains(strings.ToLower(sessions[i].Name), lower) {
				sd = &sessions[i]
				break
			}
		}
	}

	if sd == nil {
		fmt.Printf("%sSession not found: %s%s\n", Yellow, arg, Reset)
		return
	}

	// Restore model/provider if saved
	if sd.Model != "" {
		spec := api.ParseModelSpec(sd.Model)
		if p, err := api.NewProvider(spec); err == nil {
			it.provider = p
			it.ag.SetProvider(p)
		}
	}

	it.ag.SetMessages(sd.Messages)
	at := make(map[string]bool)
	for _, name := range sd.AllowedTools {
		at[name] = true
	}
	it.ag.SetAllowedTools(at)
	it.persistID = sd.ID
	it.sessionName = sd.Name
	it.nameSet = sd.NameSet
	it.createdAt = sd.CreatedAt
	it.totalCost = sd.TotalCost
	it.contextUsed = sd.ContextUsed

	switch sd.PermissionMode {
	case "Plan":
		it.ag.SetPermissionMode(agent.ModePlan)
	case "YOLO":
		it.ag.SetPermissionMode(agent.ModeYOLO)
	case "Terminal":
		it.ag.SetPermissionMode(agent.ModeTerminal)
	default:
		it.ag.SetPermissionMode(agent.ModeConfirm)
	}

	fmt.Printf("%sRestored session: %s (%d messages)%s\n", Green, sd.Name, len(sd.Messages), Reset)
}

func (it *Interactive) save() {
	if len(it.ag.Messages()) == 0 {
		return
	}
	dir := sessionsDir(it.cwd)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	info := it.provider.Info()
	data := sessionData{
		ID:             it.persistID,
		Name:           it.sessionName,
		NameSet:        it.nameSet,
		Model:          info.ProviderID + "/" + info.Model,
		Messages:       it.ag.Messages(),
		PermissionMode: it.ag.GetPermissionMode().String(),
		AllowedTools:   mapKeys(it.ag.AllowedTools()),
		TotalCost:      it.totalCost,
		ContextUsed:    it.contextUsed,
		CreatedAt:      it.createdAt,
		UpdatedAt:      time.Now(),
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	os.WriteFile(sessionFilePath(it.cwd, it.persistID), b, 0644)
}
