package otoha

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[38;5;244m"
	ansiMuted  = "\x1b[38;5;246m"
	ansiCream  = "\x1b[38;5;223m"
	ansiCyan   = "\x1b[38;5;117m"
	ansiMint   = "\x1b[38;5;158m"
	ansiAmber  = "\x1b[38;5;221m"
	ansiRose   = "\x1b[38;5;211m"
	ansiClear  = "\x1b[2J\x1b[H"
	ansiCursor = "\x1b[?25l"
	ansiShow   = "\x1b[?25h"
)

var consoleLogoFrames = []string{
	"  ╭──────────────────────────────────────────────────────────────╮\n" +
		"  │  ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░  │\n" +
		"  │      ___  _____ ___  _   _    _        SIGNAL / MEMORY     │\n" +
		"  │     / _ \\|_   _/ _ \\| | | |  / \\       O T O H A           │\n" +
		"  │    | | | | | || | | | |_| | / _ \\      collaboration core │\n" +
		"  │    | |_| | | || |_| |  _  |/ ___ \\     v0.1 / local       │\n" +
		"  │     \\___/  |_| \\___/|_| |_/_/   \\_\\                      │\n" +
		"  │  ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░  │\n" +
		"  ╰──────────────────────────────────────────────────────────────╯",
	"  ╭──────────────────────────────────────────────────────────────╮\n" +
		"  │  ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░   ░░░  │\n" +
		"  │      ___  _____ ___  _   _    _        SIGNAL / MEMORY     │\n" +
		"  │     / _ \\|_   _/ _ \\| | | |  / \\       O T O H A           │\n" +
		"  │    | | | | | || | | | |_| | / _ \\      collaboration core │\n" +
		"  │    | |_| | | || |_| |  _  |/ ___ \\     v0.1 / local       │\n" +
		"  │     \\___/  |_| \\___/|_| |_/_/   \\_\\                      │\n" +
		"  │  ·░·   ·░·   ·░·   ·░·   ·░·   ·░·   ·░·   ·░·   ·░·   ·░·  │\n" +
		"  ╰──────────────────────────────────────────────────────────────╯",
}

type consoleEvent struct {
	stamp string
	kind  string
	text  string
	color string
}

var consoleEvents = []consoleEvent{
	{stamp: "00:00.8", kind: "PLAN", text: "planner-01 published a bounded task", color: ansiCream},
	{stamp: "00:01.6", kind: "WRITE", text: "builder-02 wrote summary + body to outbox", color: ansiCyan},
	{stamp: "00:02.4", kind: "SYNC", text: "Otoha Core accepted uid 01J7… and assigned seq 42", color: ansiMint},
	{stamp: "00:03.1", kind: "ROUTE", text: "reviewer-03 received a compact envelope", color: ansiAmber},
	{stamp: "00:04.0", kind: "READ", text: "reviewer-03 fetched the body at a task boundary", color: ansiRose},
	{stamp: "00:04.8", kind: "ACK", text: "application receipt recorded: received → read", color: ansiMint},
	{stamp: "00:05.7", kind: "GRAPH", text: "shared state advanced without copying chat history", color: ansiCyan},
}

func consoleTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && f != nil && isTerminalFile(f)
}

func isTerminalFile(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func writeConsole(w io.Writer, format string, values ...any) error {
	_, err := fmt.Fprintf(w, format, values...)
	return err
}

func renderConsoleLogo(w io.Writer, frame int, clear bool) error {
	if clear {
		if err := writeConsole(w, ansiClear+ansiCursor); err != nil {
			return err
		}
	}
	if frame < 0 || frame >= len(consoleLogoFrames) {
		frame = len(consoleLogoFrames) - 1
	}
	accent := ansiCyan
	if frame%3 == 1 {
		accent = ansiMint
	} else if frame%3 == 2 {
		accent = ansiCream
	}
	return writeConsole(w, "%s%s%s\n", accent, consoleLogoFrames[frame], ansiReset)
}

func renderConsoleDashboard(w io.Writer, events int) error {
	if err := writeConsole(w, "\n%s  OTOHA / COLLABORATION CONSOLE%s  %s● local core%s\n\n", ansiCream, ansiReset, ansiMint, ansiReset); err != nil {
		return err
	}
	if err := writeConsole(w, "  %s┌─ CORE ─────────────────┐  ┌─ CHANNELS ───────────────┐  ┌─ AGENTS ─────────────────┐%s\n", ansiDim, ansiReset); err != nil {
		return err
	}
	if err := writeConsole(w, "  %s│%s  %sREADY%s  SQLite + Go      %s│%s  %s●%s server / local         %s│%s  %s05%s active / %s02%s idle       %s│%s\n", ansiDim, ansiReset, ansiMint, ansiReset, ansiDim, ansiReset, ansiMint, ansiReset, ansiDim, ansiReset, ansiDim, ansiReset, ansiCyan, ansiReset, ansiDim, ansiReset); err != nil {
		return err
	}
	if err := writeConsole(w, "  %s│%s  uid dedupe  ·  receipts  %s│%s  queued → routed → read    %s│%s  planner  builder  reviewer %s│%s\n", ansiDim, ansiReset, ansiDim, ansiReset, ansiDim, ansiReset, ansiDim, ansiReset); err != nil {
		return err
	}
	if err := writeConsole(w, "  %s└────────────────────────┘  └──────────────────────────┘  └──────────────────────────┘%s\n\n", ansiDim, ansiReset); err != nil {
		return err
	}
	if err := writeConsole(w, "  %sLIVE EVENT STREAM%s  %s· complete messages persist; notifications stay small%s\n", ansiCream, ansiReset, ansiMuted, ansiReset); err != nil {
		return err
	}
	visible := events
	if visible > len(consoleEvents) {
		visible = len(consoleEvents)
	}
	for i := 0; i < visible; i++ {
		e := consoleEvents[i]
		if err := writeConsole(w, "  %s%s%s  %s%-6s%s  %s\n", ansiMuted, e.stamp, ansiReset, e.color, e.kind, ansiReset, e.text); err != nil {
			return err
		}
	}
	if visible == 0 {
		if err := writeConsole(w, "  %s— waiting for the first envelope —%s\n", ansiMuted, ansiReset); err != nil {
			return err
		}
	}
	return writeConsole(w, "\n  %s↑%s  press %senter%s to send a task  ·  %sd%s to play the story  ·  %s?%s for help  ·  %sq%s to exit\n", ansiMint, ansiReset, ansiCyan, ansiReset, ansiCream, ansiReset, ansiAmber, ansiReset, ansiRose, ansiReset)
}

// RunConsoleDemo plays the compact, deterministic story used by the terminal
// recording. It is intentionally based on the real Otoha message vocabulary,
// while the data is synthetic and safe to run without credentials.
func RunConsoleDemo(w io.Writer, slow bool) error {
	delay := 260 * time.Millisecond
	if slow {
		delay = 900 * time.Millisecond
	}
	for i := 1; i <= len(consoleEvents); i++ {
		if consoleTTY(w) {
			if err := writeConsole(w, ansiClear); err != nil {
				return err
			}
		}
		if err := renderConsoleLogo(w, i%len(consoleLogoFrames), false); err != nil {
			return err
		}
		if err := renderConsoleDashboard(w, i); err != nil {
			return err
		}
		time.Sleep(delay)
	}
	return writeConsole(w, "\n  %sDEMO COMPLETE%s  %s7 events · 0 lost · 2 receipts · 1 shared state update%s\n", ansiMint, ansiReset, ansiMuted, ansiReset)
}

func consoleTeams(value any) []Object {
	switch rows := value.(type) {
	case []Object:
		return rows
	case []any:
		var result []Object
		for _, row := range rows {
			if object, ok := row.(map[string]any); ok {
				result = append(result, Object(object))
			} else if object, ok := row.(Object); ok {
				result = append(result, object)
			}
		}
		return result
	}
	return nil
}

func renderLocalConsoleState(out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := LocalStatus(ctx)
	if err != nil {
		return writeConsole(out, "Otoha · 本机概览\n\n本机状态读取失败：%v\n检查：otoha doctor\n", err)
	}
	teams := consoleTeams(state["teams"])
	if err := writeConsole(out, "Otoha · 本机概览\n\n"); err != nil {
		return err
	}
	if len(teams) == 0 {
		return writeConsole(out, "未接入项目。\n在终端运行：otoha join --invite FILE\n")
	}
	if err := writeConsole(out, "%d 个项目\n", len(teams)); err != nil {
		return err
	}
	for _, team := range teams {
		name := S(team, "name")
		if name == "" {
			name = S(team, "team")
		}
		connection := "连接状态未检查"
		switch S(team, "connection") {
		case "online":
			connection = "已连接"
		case "offline":
			connection = "离线"
		case "connecting":
			connection = "正在连接"
		}
		if S(team, "error") != "" {
			connection = "本地配置需要处理"
		} else if S(team, "identity_state") == "missing" {
			connection = "本地身份文件缺失"
		}
		if err := writeConsole(out, "  %s · %s\n", name, connection); err != nil {
			return err
		}
	}
	receiver := O(O(state, "daemon"), "receiver")
	background := "运行状态未知"
	if alive, known := receiver["alive"].(bool); known {
		if alive {
			background = "本机进程存在"
		} else if S(receiver, "status_file") == "missing" {
			background = "无运行记录"
		} else {
			background = "未检测到运行进程"
		}
	}
	if err := writeConsole(out, "\n后台：%s\n\n在终端运行：\n", background); err != nil {
		return err
	}
	teamArgument := "TEAM"
	if len(teams) == 1 {
		teamArgument = S(teams[0], "name")
		if teamArgument == "" {
			teamArgument = S(teams[0], "team")
		}
	}
	return writeConsole(out, "  otoha page open --team %s --browser\n  otoha message list --team %s\n  otoha status\n", teamArgument, teamArgument)
}

func runLocalConsoleLoop(in io.Reader, out io.Writer, tty bool) error {
	scanner := bufio.NewScanner(in)
	for {
		if err := writeConsole(out, "\notoha> "); err != nil {
			return err
		}
		if !scanner.Scan() {
			return scanner.Err()
		}
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "", "status":
			if err := renderLocalConsoleState(out); err != nil {
				return err
			}
		case "d", "demo", "run":
			if err := RunConsoleDemo(out, false); err != nil {
				return err
			}
		case "slow":
			if err := RunConsoleDemo(out, true); err != nil {
				return err
			}
		case "?", "help":
			if err := writeConsole(out, "\nstatus  重新读取本机状态\ndemo    播放虚构协作演示\nslow    慢速播放同一演示\nclear   清屏\nq       退出\n\n退出后在终端运行：\n  otoha join --invite FILE\n  otoha page help\n  otoha message help\n"); err != nil {
				return err
			}
		case "clear":
			if tty {
				if err := writeConsole(out, ansiClear); err != nil {
					return err
				}
			}
		case "q", "quit", "exit":
			return nil
		default:
			if err := writeConsole(out, "输入 ? 查看本机控制台命令，或输入 q 退出后运行 otoha 子命令。\n"); err != nil {
				return err
			}
		}
	}
}

// Console is a read-only local overview. The explicit demo command keeps the
// synthetic recording, while normal startup reports observed local state.
func Console(in io.Reader, out io.Writer) error {
	if err := renderLocalConsoleState(out); err != nil {
		return err
	}
	if !consoleTTY(out) {
		return nil
	}
	if err := writeConsole(out, "\n输入 ? 查看控制台命令，q 返回终端。\n"); err != nil {
		return err
	}
	return runLocalConsoleLoop(in, out, true)
}
