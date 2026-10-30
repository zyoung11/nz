package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"nz/internal/protocol"
	"nz/internal/state"
)

const colorRedSoft = "\x1b[38;2;237;135;150m"

func statusColor(s string) string {
	switch s {
	case "local":
		return colorBlue
	case "active", "online":
		return colorGreen
	case "pending":
		return colorYellow
	case "blocked", "offline":
		return colorRedSoft
	case "unknown":
		return colorYellow
	default:
		return colorReset
	}
}

func printMachineTable(peers []protocol.PeerInfo) {
	header := []string{"Name", "User", "VPN IP", "Status", "Trust"}
	rows := make([][]string, 0, len(peers))
	for _, m := range peers {
		status := m.Status
		if m.Self {
			status = "local"
		}
		rows = append(rows, []string{m.Machine, m.User, dash(m.VPNIP), status, yesNo(m.Trusted)})
	}
	printTable(header, rows, func(rowIdx, colIdx int, _ string) string {
		if colIdx == 3 {
			return statusColor(rows[rowIdx][3])
		}
		return colorReset
	})
}

func printUserTable(users []state.User, machines []state.MachineView, pendingOnly bool) {
	type rowDef struct {
		user     string
		status   string
		machines []state.MachineView
	}
	groups := make([]rowDef, 0, len(users))
	for _, u := range users {
		list := make([]state.MachineView, 0)
		for _, m := range machines {
			if m.User == u.Name {
				list = append(list, m)
			}
		}
		if pendingOnly {
			filtered := list[:0:0]
			for _, m := range list {
				if m.Status == state.MachinePending {
					filtered = append(filtered, m)
				}
			}
			list = filtered
			if u.Status != state.UserPending && len(list) == 0 {
				continue
			}
		}
		groups = append(groups, rowDef{user: u.Name, status: string(u.Status), machines: list})
	}

	fmt.Println()
	overview := make([][]string, 0, len(groups))
	for _, g := range groups {
		overview = append(overview, []string{g.status, g.user, fmt.Sprintf("%d", len(g.machines))})
	}
	printTable([]string{"State", "User", "Machines"}, overview, func(rowIdx, colIdx int, _ string) string {
		if colIdx == 0 {
			return statusColor(overview[rowIdx][0])
		}
		return colorReset
	})

	for _, g := range groups {
		fmt.Printf("\n%s%s%s\n", colorBold+colorMauve, g.user, colorReset)
		if len(g.machines) == 0 {
			fmt.Println("  no machines")
			continue
		}
		header := []string{"Machine", "VPN IP", "Status", "Key", "Last Seen", "Trust"}
		rows := make([][]string, 0, len(g.machines))
		for _, m := range g.machines {
			status := string(m.Status)
			if m.Self {
				status = "local"
			}
			rows = append(rows, []string{
				m.Name, dash(m.VPNIP), status, dash(m.Fingerprint),
				formatSeen(m.LastSeen), yesNo(m.Trusted),
			})
		}
		printTable(header, rows, func(rowIdx, colIdx int, _ string) string {
			if colIdx == 2 {
				return statusColor(rows[rowIdx][2])
			}
			return colorReset
		})
	}
	fmt.Println()
}

func printTable(header []string, rows [][]string, color func(rowIdx, colIdx int, value string) string) {
	widths := columnWidths(header, rows)
	fmt.Println(rule(widths, "┌", "┬", "┐", "─"))
	fmt.Println(coloredRow(header, widths, func(int, string) string { return colorBlue }))
	fmt.Println(rule(widths, "├", "┼", "┤", "─"))
	for i, r := range rows {
		fmt.Println(coloredRow(r, widths, func(colIdx int, value string) string {
			return color(i, colIdx, value)
		}))
	}
	fmt.Println(rule(widths, "└", "┴", "┘", "─"))
}

func coloredRow(cells []string, widths []int, color func(colIdx int, value string) string) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		pad := max(widths[i]-runewidth.StringWidth(c), 0)
		parts[i] = " " + color(i, c) + c + strings.Repeat(" ", pad) + colorReset + " "
	}
	return "│" + strings.Join(parts, "│") + "│"
}

func rule(widths []int, left, mid, right, fill string) string {
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat(fill, w+2)
	}
	return left + strings.Join(parts, mid) + right
}

func columnWidths(header []string, rows [][]string) []int {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = runewidth.StringWidth(h)
	}
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], runewidth.StringWidth(c))
		}
	}
	return widths
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func formatSeen(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("01-02 15:04")
}
