// init.go implements the read-only AI-assisted `ckb-whitehat init` workflow:
// fail-fast agent CLI check -> target clone/workspace setup -> read-only agent
// inspection (LivePanel on TTY) -> host prerequisite verification -> per-command
// confirmation & confined execution -> setup.json persistence for `run`.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nghiadang391/ckb-whitehat/internal/agent"
	"github.com/nghiadang391/ckb-whitehat/internal/chain"
	"github.com/nghiadang391/ckb-whitehat/internal/detect"
	"github.com/nghiadang391/ckb-whitehat/internal/gates"
	"github.com/nghiadang391/ckb-whitehat/internal/setup"
	"github.com/nghiadang391/ckb-whitehat/internal/workspace"
)

const (
	defaultInitStallTimeout = 120
	defaultInitMaxRuntime   = 300
)

var initPipelineStages = []pipelineStage{
	{id: "target", label: "Target"},
	{id: "inspect", label: "Inspect"},
	{id: "confirm", label: "Setup"},
	{id: "ready", label: "Ready"},
}

func initCmd(a []string) int {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	// Fail fast if no agent CLI is on PATH: README documents that ckb-whitehat
	// requires `claude` or `codex`, and init uses it in read-only mode to
	// inspect arbitrary target stacks.
	agents := agent.Detect()
	name, _ := flagVal(a, "agent")
	agentSpec, err := agent.Find(agents, name)
	if err != nil {
		die(err.Error(), 2)
	}

	m := workspace.New("")
	scope := scopeFrom(a)
	if len(scope) == 0 {
		scope = []string{"app"}
	}
	ro := resolveOpts(a)
	explicitInstall := ro.Install
	noInstall := hasFlag(a, "no-install-deps") || hasFlag(a, "auto-skip-gates")
	allowScripts := hasFlag(a, "allow-scripts")
	// Dependency installation is handled below via read-only agent inspection +
	// explicit per-command approval rather than blind npm install during clone.
	ro.Install = false

	ws, cfg, err := m.InitOpts(targetFrom(a), scope, hasFlag(a, "chain"), ro)
	if err != nil {
		die(err.Error(), 2)
	}

	fmt.Printf("initialized workspace: %s\n", ws)
	fmt.Printf("  target : %s\n", cfg.Target)
	suffix := ""
	if cfg.Onchain {
		suffix = " (+ on-chain evidence)"
	}
	fmt.Printf("  scope  : %s%s\n", strings.Join(cfg.Scope, ", "), suffix)
	fmt.Printf("  agent  : %s (read-only inspection)\n", agentSpec.Name)

	stall := secondsFlag(a, "stall-timeout", "CKB_WHITEHAT_STALL_TIMEOUT", defaultInitStallTimeout)
	maxRuntime := secondsFlag(a, "max-runtime", "CKB_WHITEHAT_MAX_RUNTIME", defaultInitMaxRuntime)

	panelActive := IsTerminal()
	var paintMu sync.Mutex
	var panelVisible bool
	var panel *LivePanel
	if panelActive {
		panel = NewLivePanel(fmt.Sprintf("init · %s · %s", agentSpec.Name, filepath.Base(cfg.Target)), 0, stall)
		panel.SetStages(initPipelineStages)
		panel.SetStage("inspect")
		panel.SetSize(TerminalSize())
		stopResize := WatchResize(func(cols, rows int) {
			paintMu.Lock()
			defer paintMu.Unlock()
			panel.SetSize(cols, rows)
			if panelVisible {
				_, _ = os.Stdout.WriteString(panel.Teardown() + panel.Render())
			}
		})
		defer stopResize()
	}

	rec := setup.Record{
		InspectedAt:  time.Now().UTC().Format(time.RFC3339),
		Agent:        string(agentSpec.Name),
		AllowScripts: allowScripts,
	}

	// Run the read-only init agent (Read/Glob/Grep/LS only; zero Bash/Write).
	initPrompt := setup.BuildInitPrompt(cfg.Target, cfg.Scope, allowScripts)
	argv := agent.ReadOnlyArgs(agentSpec, setup.ReadOnlyTools, cfg.Target, initPrompt)
	env := agent.Env(os.Environ(), ws, cfg.Target)
	res, err := spawnWithLivePanel(ctx, agentSpec, argv, env, ws, stall, maxRuntime, panelActive, panel, &paintMu, &panelVisible)
	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "\ninterrupted")
		return 1
	}
	if err != nil {
		die(err.Error(), 1)
	}

	// Persist init.log for debugging inspection output & tool activity.
	writeInitLog(filepath.Join(ws, "init.log"), res)

	switch {
	case res.TimedOut:
		rec.InspectError = fmt.Sprintf("init inspection exceeded %ds wall-clock limit", int(maxRuntime.Seconds()))
		fmt.Fprintf(os.Stderr, "warning: %s (see %s)\n", rec.InspectError, filepath.Join(ws, "init.log"))
	case res.Stalled:
		rec.InspectError = fmt.Sprintf("init inspection stalled after %ds", int(stall.Seconds()))
		fmt.Fprintf(os.Stderr, "warning: %s (see %s)\n", rec.InspectError, filepath.Join(ws, "init.log"))
	case res.Code != 0:
		rec.InspectError = fmt.Sprintf("init inspection agent exited %d", res.Code)
		fmt.Fprintf(os.Stderr, "warning: %s (see %s)\n", rec.InspectError, filepath.Join(ws, "init.log"))
	default:
		if plan, perr := setup.ParsePlan(res.Stdout); perr != nil {
			rec.InspectError = perr.Error()
			fmt.Fprintf(os.Stderr, "warning: init inspection did not return a structured setup plan (%v; see %s)\n", perr, filepath.Join(ws, "init.log"))
		} else {
			rec.Plan = *plan
		}
	}

	// Check host tools requested by the plan and detect running local CKB/Fiber
	// services (skip un-credentialed host Postgres port 5432 probing).
	rec.ToolChecks = setup.CheckTools(rec.Plan.Tools, nil)
	cfg.Services = detect.Detect(ctx, detect.Options{PGPorts: []int{}})
	if err := m.SaveConfig(ws, cfg); err != nil {
		die(err.Error(), 1)
	}

	printInitInspection(rec)

	// Per-command approval & execution for suggested target dependency commands.
	isManaged := m.IsManagedClone(cfg.Target)
	anyInstalled := false
	if len(rec.Plan.Commands) > 0 {
		outcomes, installed := handleSetupCommands(ctx, cfg.Target, rec.Plan.Commands, allowScripts, isManaged, explicitInstall, noInstall, panelActive, panel, &paintMu, &panelVisible)
		rec.Outcomes = outcomes
		anyInstalled = installed
		if ctx.Err() != nil {
			fmt.Fprintln(os.Stderr, "\ninterrupted")
			return 1
		}
	}

	if anyInstalled {
		cfg.DepsInstalled = true
		if err := m.SaveConfig(ws, cfg); err != nil {
			die(err.Error(), 1)
		}
	}

	// Bonus: if the read-only init agent detected a CKB/Fiber project and
	// --chain was not passed yet, offer to enable chain scope & auto-start the
	// local Docker devnet harness (or print a clear hint in non-TTY mode).
	noChainStart := hasFlag(a, "no-chain-start")
	if !cfg.Onchain && detectCKBFiberStack(&rec) {
		if panelActive && !noInstall && !noChainStart {
			if promptEnableChainPanel(panel, &paintMu, &panelVisible) {
				cfg.Onchain = true
				if !contains(cfg.Scope, "chain") {
					cfg.Scope = append(cfg.Scope, "chain")
				}
				if err := m.SaveConfig(ws, cfg); err != nil {
					die(err.Error(), 1)
				}
			}
		} else {
			fmt.Println("\nhint: detected CKB/Fiber stack — pass --chain to auto-start the local devnet + 2-node FNN harness and enable on-chain verification.")
		}
	}

	// Option 1: auto-start local Docker CKB-devnet + 2-node FNN harness on --chain
	// (no separate `ckb-whitehat chain up` needed).
	if cfg.Onchain && !noChainStart {
		manifest, mpath, cerr := ensureDevnetChain(ctx, m.Home, panelActive || hasFlag(a, "chain"))
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not start local CKB/Fiber devnet harness: %v\n", cerr)
			fmt.Fprintln(os.Stderr, "         start Docker and run `ckb-whitehat chain up` before `run --chain`.")
		} else if manifest != nil {
			fmt.Println("\nlocal CKB/Fiber devnet harness:")
			printChainSummary(manifest, mpath)
			cfg.Services = detect.Detect(ctx, detect.Options{PGPorts: []int{}})
			_ = m.SaveConfig(ws, cfg)
		}
	}

	if err := setup.SaveRecord(ws, rec); err != nil {
		die(err.Error(), 1)
	}

	printPrerequisitesWithSetup(cfg, rec)
	fmt.Printf("\n  setup  : %s\n", setup.RecordPath(ws))
	if cfg.Onchain {
		fmt.Printf("  next   : ckb-whitehat run --target %s --chain\n", cfg.Target)
	} else {
		fmt.Printf("  next   : ckb-whitehat run --target %s\n", cfg.Target)
	}
	return 0
}

func promptEnableChainPanel(p *LivePanel, paintMu *sync.Mutex, panelVisible *bool) bool {
	lines := []promptLine{
		{text: "CKB / Fiber stack detected in this repository.", tone: "cyan"},
		{text: "Enable --chain scope and auto-start the local Docker CKB-devnet + 2-node FNN harness?", tone: ""},
		{text: "(Zero pre-funded testnet addresses needed; includes CKB + TEST_XUDT channels.)", tone: "dim"},
	}
	paintMu.Lock()
	p.SetStage("confirm")
	p.SetSize(TerminalSize())
	cols, _ := TerminalSize()
	bar := " " + styleBold("a") + " enable --chain & start devnet  " + styleBold("d") + " keep app-only"
	p.SetCommandPrompt("Chain Verification", lines, truncateDisplay(bar, cols))
	fmt.Print("\x1b[?25l")
	_, _ = os.Stdout.WriteString(p.Teardown() + p.Render())
	*panelVisible = true
	paintMu.Unlock()

	key, err := ReadSingleKey()

	paintMu.Lock()
	*panelVisible = false
	p.SetCommandPrompt("", nil, "")
	_, _ = os.Stdout.WriteString(p.Teardown())
	fmt.Print("\x1b[?25h")
	paintMu.Unlock()

	return err == nil && key == 'a'
}

func spawnWithLivePanel(ctx context.Context, spec agent.Spec, argv, env []string, cwd string, stall, maxRuntime time.Duration, panelActive bool, panel *LivePanel, paintMu *sync.Mutex, panelVisible *bool) (agent.Result, error) {
	var ticker *time.Ticker
	var stopRepaint, repaintDone chan struct{}
	observer := func(string) {}
	if panelActive && panel != nil {
		observer = panel.OnLine
		paintMu.Lock()
		panel.Restart()
		panel.SetSize(TerminalSize())
		_, _ = os.Stdout.WriteString(panel.Teardown())
		fmt.Print("\x1b[?25l")
		_, _ = os.Stdout.WriteString(panel.Render())
		*panelVisible = true
		paintMu.Unlock()
		ticker = time.NewTicker(100 * time.Millisecond)
		stopRepaint, repaintDone = make(chan struct{}), make(chan struct{})
		go func() {
			defer close(repaintDone)
			for {
				select {
				case <-stopRepaint:
					return
				case <-ticker.C:
					paintMu.Lock()
					if *panelVisible {
						panel.SetSize(TerminalSize())
						_, _ = os.Stdout.WriteString(panel.Teardown() + panel.Render())
					}
					paintMu.Unlock()
				}
			}
		}()
	}

	res, err := agent.SpawnWithObserver(ctx, spec, argv, env, cwd, stall, maxRuntime, observer)

	if panelActive && panel != nil {
		close(stopRepaint)
		<-repaintDone
		ticker.Stop()
		paintMu.Lock()
		*panelVisible = false
		_, _ = os.Stdout.WriteString(panel.Teardown())
		fmt.Print("\x1b[?25h")
		paintMu.Unlock()
	}
	return res, err
}

func writeInitLog(path string, res agent.Result) {
	content := res.Stdout
	if res.Activity != "" {
		content += "\n--- activity ---\n" + res.Activity
	}
	if res.Stderr != "" {
		content += "\n--- stderr ---\n" + res.Stderr
	}
	_ = os.WriteFile(path, []byte(content+"\n"), 0644)
}

func printInitInspection(rec setup.Record) {
	if len(rec.Plan.Stacks) == 0 && rec.Plan.Summary == "" && len(rec.Plan.Notes) == 0 {
		return
	}
	fmt.Println("\ntarget inspection:")
	if len(rec.Plan.Stacks) > 0 {
		fmt.Printf("  stack     %s\n", strings.Join(rec.Plan.Stacks, ", "))
	}
	if rec.Plan.Summary != "" {
		fmt.Printf("  summary   %s\n", rec.Plan.Summary)
	}
	for _, n := range rec.Plan.Notes {
		fmt.Printf("  note      %s\n", n)
	}
}

func handleSetupCommands(ctx context.Context, target string, cmds []setup.SetupCommand, allowScripts, isManaged, explicitInstall, noInstall, panelActive bool, panel *LivePanel, paintMu *sync.Mutex, panelVisible *bool) ([]setup.CommandOutcome, bool) {
	outcomes := make([]setup.CommandOutcome, 0, len(cmds))
	anyInstalled := false

	// Determine whether interactive execution is allowed for this invocation.
	var skipReason string
	var defaultStatus string
	switch {
	case noInstall:
		defaultStatus = "skipped"
		skipReason = "skipped (--no-install-deps)"
	case !isManaged && !explicitInstall:
		defaultStatus = "skipped"
		skipReason = fmt.Sprintf("external local directory — pass --install-deps to approve and run setup commands in %s", target)
	case !panelActive:
		defaultStatus = "denied"
		skipReason = "non-interactive (no TTY) — auto-denied for safety"
	}

	if skipReason != "" {
		fmt.Printf("\nsuggested setup commands (%s):\n", skipReason)
		for _, sc := range cmds {
			warnings := setup.AnalyzeCommandRisk(sc.Command, allowScripts)
			if _, err := setup.ConfineDir(target, sc.Dir); err != nil {
				warnings = append(warnings, err.Error())
			}
			o := setup.CommandOutcome{
				Command:  sc.Command,
				Dir:      sc.Dir,
				Reason:   sc.Reason,
				Warnings: warnings,
				Status:   defaultStatus,
			}
			outcomes = append(outcomes, o)
			fmt.Printf("  [%s] (%s) %s\n", o.Status, gates.SanitizeSingleLine(sc.Dir), gates.SanitizeSingleLine(sc.Command))
			if sc.Reason != "" {
				fmt.Printf("           reason: %s\n", gates.SanitizeSingleLine(sc.Reason))
			}
			for _, w := range warnings {
				fmt.Printf("           warning: %s\n", gates.SanitizeSingleLine(w))
			}
		}
		return outcomes, false
	}

	// Interactive TTY per-command approval via LivePanel.
	skipRemaining := false
	for i, sc := range cmds {
		warnings := setup.AnalyzeCommandRisk(sc.Command, allowScripts)
		_, confineErr := setup.ConfineDir(target, sc.Dir)
		if confineErr != nil {
			warnings = append(warnings, confineErr.Error())
		}
		if skipRemaining {
			outcomes = append(outcomes, setup.CommandOutcome{
				Command:  sc.Command,
				Dir:      sc.Dir,
				Reason:   sc.Reason,
				Warnings: warnings,
				Status:   "skipped",
			})
			continue
		}
		if confineErr != nil {
			fmt.Fprintf(os.Stderr, "blocked unsafe setup command (dir=%s): %v\n", gates.SanitizeSingleLine(sc.Dir), confineErr)
			outcomes = append(outcomes, setup.CommandOutcome{
				Command:  sc.Command,
				Dir:      sc.Dir,
				Reason:   sc.Reason,
				Warnings: warnings,
				Status:   "denied",
				Error:    confineErr.Error(),
			})
			continue
		}

		choice := promptSetupCommandPanel(panel, paintMu, panelVisible, i+1, len(cmds), sc, warnings)
		switch choice {
		case "approve":
			fmt.Printf("\nrunning setup command (%d/%d) in %s: %s\n", i+1, len(cmds), gates.SanitizeSingleLine(sc.Dir), gates.SanitizeSingleLine(sc.Command))
			code, err := setup.RunCommand(ctx, target, sc, os.Stdout)
			o := setup.CommandOutcome{
				Command:  sc.Command,
				Dir:      sc.Dir,
				Reason:   sc.Reason,
				Warnings: warnings,
				ExitCode: code,
			}
			if err != nil {
				o.Status = "failed"
				o.Error = err.Error()
				fmt.Fprintf(os.Stderr, "setup command failed (exit %d): %v\n", code, err)
			} else {
				o.Status = "approved"
				anyInstalled = true
			}
			outcomes = append(outcomes, o)
		case "skip":
			skipRemaining = true
			outcomes = append(outcomes, setup.CommandOutcome{
				Command:  sc.Command,
				Dir:      sc.Dir,
				Reason:   sc.Reason,
				Warnings: warnings,
				Status:   "skipped",
			})
		default:
			outcomes = append(outcomes, setup.CommandOutcome{
				Command:  sc.Command,
				Dir:      sc.Dir,
				Reason:   sc.Reason,
				Warnings: warnings,
				Status:   "denied",
			})
		}
	}
	return outcomes, anyInstalled
}

func promptSetupCommandPanel(p *LivePanel, paintMu *sync.Mutex, panelVisible *bool, idx, total int, sc setup.SetupCommand, warnings []string) string {
	safeCmd := gates.SanitizeSingleLine(sc.Command)
	safeDir := gates.SanitizeSingleLine(sc.Dir)
	safeReason := gates.SanitizeSingleLine(sc.Reason)
	safeWarnings := make([]string, 0, len(warnings))
	for _, w := range warnings {
		safeWarnings = append(safeWarnings, gates.SanitizeSingleLine(w))
	}

	// Put risk warnings ahead of reason so tight terminals never drop warnings.
	lines := []promptLine{
		{text: "$ " + safeCmd, tone: "cyan"},
		{text: "dir: " + safeDir, tone: "dim"},
	}
	for _, w := range safeWarnings {
		lines = append(lines, promptLine{text: "⚠ " + w, tone: "yellow"})
	}
	if safeReason != "" {
		lines = append(lines, promptLine{text: "why: " + safeReason, tone: ""})
	}
	title := fmt.Sprintf("Setup Command (%d/%d)", idx, total)

	paintMu.Lock()
	// Always print the complete untruncated command and every risk warning
	// above the approval box so long arguments or warnings can never be hidden
	// behind TUI box wrapping/clipping before the user approves.
	_, _ = os.Stdout.WriteString(p.Teardown())
	fmt.Printf("\n[%s] dir=%s\n  $ %s\n", title, safeDir, safeCmd)
	for _, w := range safeWarnings {
		fmt.Printf("  WARNING: %s\n", w)
	}
	p.SetStage("confirm")
	p.SetSize(TerminalSize())
	cols, _ := TerminalSize()
	p.SetCommandPrompt(title, lines, renderSetupActionBar(cols))
	fmt.Print("\x1b[?25l")
	_, _ = os.Stdout.WriteString(p.Render())
	*panelVisible = true
	paintMu.Unlock()

	key, err := ReadSingleKey()

	paintMu.Lock()
	*panelVisible = false
	p.SetCommandPrompt("", nil, "")
	_, _ = os.Stdout.WriteString(p.Teardown())
	fmt.Print("\x1b[?25h")
	paintMu.Unlock()

	if err != nil {
		return "skip"
	}
	switch key {
	case 'a':
		return "approve"
	case 's', 'x':
		return "skip"
	case 'd':
		return "deny"
	default:
		return "deny"
	}
}

func printPrerequisitesWithSetup(cfg workspace.Config, rec setup.Record) {
	ctx := context.Background()
	node := detect.NodeVersion(ctx, detect.Options{})
	lines := cfg.Services
	if lines == nil {
		lines = detect.Detect(ctx, detect.Options{})
	}
	fnnUp := false
	pgUp := false
	for _, l := range lines {
		if strings.Contains(l, "Fiber (FNN) node is running") {
			fnnUp = true
		}
		if strings.Contains(l, "Postgres is running") {
			pgUp = true
		}
	}

	fmt.Println("\nprerequisites:")
	if node != "" {
		fmt.Printf("  ok        node %s (PoCs are agent-written and run under Node)\n", node)
	} else {
		fmt.Println("  missing   node - agent-written PoCs cannot run; install Node 18+ and re-init")
	}

	// Print host tool checks discovered by the read-only init agent (skip
	// duplicating "node" which is already reported with its version above).
	for _, tc := range rec.ToolChecks {
		if tc.Name == "node" {
			continue
		}
		if tc.Present {
			if tc.Reason != "" {
				fmt.Printf("  ok        %s (%s)\n", tc.Name, tc.Reason)
			} else {
				fmt.Printf("  ok        %s\n", tc.Name)
			}
		} else {
			msg := fmt.Sprintf("  missing   %s", tc.Name)
			if tc.Reason != "" {
				msg += " - " + tc.Reason
			}
			fmt.Println(msg)
			if tc.Install != "" {
				fmt.Printf("            install: %s\n", tc.Install)
			}
		}
	}

	// Report live node_modules state if target has package.json.
	hasPkgJSON, hasNodeModules := targetDepsState(cfg.Target)
	if hasPkgJSON {
		if hasNodeModules {
			fmt.Println("  ok        target node_modules present")
		} else {
			fmt.Println("  warning   target has package.json but no node_modules; PoCs importing npm packages will fail")
			if cfg.TargetOrigin == "clone" && cfg.CloneURL != "" {
				fmt.Printf("            re-run init interactively to approve dependency install: ckb-whitehat init %s\n", cfg.CloneURL)
			} else {
				fmt.Printf("            pass --install-deps to approve dependency install: ckb-whitehat init %s --install-deps\n", cfg.Target)
			}
		}
	}

	// Print local service status + agent-discovered service hints.
	if chain.ScratchPostgresReady(ctx) {
		pgUp = true
		fmt.Printf("  ok        isolated Docker scratch Postgres ready (%s)\n", chain.ScratchPGURL())
	} else if needsPostgres(cfg.Target, &rec) {
		if chain.DockerAvailable(ctx) == nil {
			fmt.Println("  ok        Docker available (isolated scratch Postgres will auto-start on 127.0.0.1:5439 during `run`)")
		} else {
			fmt.Println("  warning   target uses Postgres, but Docker is unavailable; set CKB_WHITEHAT_POSTGRES_URL before `run` for DB-backed PoCs")
		}
	}
	for _, sh := range rec.Plan.Services {
		lower := strings.ToLower(sh.Name)
		if strings.Contains(lower, "postgres") && pgUp {
			continue
		}
		if (strings.Contains(lower, "fiber") || strings.Contains(lower, "fnn")) && fnnUp {
			continue
		}
		line := fmt.Sprintf("  service   %s", sh.Name)
		if sh.Reason != "" {
			line += " - " + sh.Reason
		}
		fmt.Println(line)
		if sh.Hint != "" {
			fmt.Printf("            hint: %s\n", sh.Hint)
		}
	}

	if cfg.Onchain {
		h := chain.NewHarness("")
		st := h.Status(ctx)
		if st.Ready() {
			fmt.Printf("  ok        CKB devnet (tip=%d) + 2 FNN nodes + %d channel(s) ready\n", st.CKBTip, len(st.Channels))
		} else if fnnUp {
			fmt.Println("  ok        Fiber (FNN) node detected (on-chain evidence layer is ready)")
		} else {
			fmt.Println("  warning   chain scope selected but no local CKB/Fiber devnet harness is running")
			fmt.Println("            run `ckb-whitehat chain up` (requires Docker) or pass --chain during run to auto-start it.")
		}
	}
}
