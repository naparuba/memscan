package deck

import (
	"slices"
	"testing"
)

const testReaperCommand = "/home/deck/.local/share/Steam/ubuntu12_32/reaper SteamLaunch AppId=4072721196 -- /usr/bin/flatpak run"

func proc(pid, ppid int, comm, command string, state ProcessState) *Process {
	return &Process{PID: pid, PPID: ppid, Comm: comm, Command: command, State: state}
}

// usableExceptZombies stands for isGameProcess without reading /proc.
func usableExceptZombies(p *Process) bool {
	return p.State != 'Z' && p.Comm != "xdg-dbus-proxy"
}

func pids(processes []*Process) (list []int) {
	for _, p := range processes {
		list = append(list, p.PID)
	}
	return
}

// The game was started a second time: the reaper of the first launch is still there, its game
// is a zombie, and it has the lowest PID. The game is below the newest reaper.
func TestGameIsFoundBelowTheNewestReaper(t *testing.T) {
	processes := []*Process{
		// the first launch, 6 hours ago
		proc(20305, 1, "reaper", testReaperCommand, 'S'),
		proc(20329, 20305, "bwrap", "bwrap --args 85 -- xdg-dbus-proxy", 'S'),
		proc(20330, 20329, "xdg-dbus-proxy", "xdg-dbus-proxy --args=87", 'S'),
		proc(20333, 20305, "bwrap", "bwrap --args 85 -- bottles-cli run", 'S'),
		proc(20480, 20333, "Ace7Game.exe", "Ace7Game.exe", 'Z'),
		// the second launch
		proc(21778, 1, "reaper", testReaperCommand, 'S'),
		proc(21779, 21778, "bwrap", "bwrap --args 85 -- bottles-cli run", 'S'),
		proc(21788, 21779, "bwrap", "bwrap --args 85 -- bottles-cli run", 'S'),
		proc(21887, 21788, "Ace7Game.exe", "Ace7Game.exe", 'S'),
	}
	found := gameProcessesOf(processes, 4072721196, usableExceptZombies)
	if !slices.Equal(pids(found), []int{21887}) {
		t.Fatalf("found %v, want [21887]", pids(found))
	}
	if found[0].AppID != 4072721196 {
		t.Fatalf("AppID %d", found[0].AppID)
	}
}

// Nothing below the newest reaper yet (the game is starting): the older launch is still a
// valid answer when its game is alive.
func TestOlderReaperIsUsedWhenTheNewestHasNoGame(t *testing.T) {
	processes := []*Process{
		proc(100, 1, "reaper", testReaperCommand, 'S'),
		proc(101, 100, "bwrap", "wrapper", 'S'),
		proc(102, 101, "bwrap", "adverb", 'S'),
		proc(103, 102, "game.exe", "game.exe", 'S'),
		proc(200, 1, "reaper", testReaperCommand, 'S'),
	}
	if found := gameProcessesOf(processes, 4072721196, usableExceptZombies); !slices.Equal(pids(found), []int{103}) {
		t.Fatalf("found %v, want [103]", pids(found))
	}
}

func TestNoGameWhenNoReaperMatches(t *testing.T) {
	processes := []*Process{
		proc(100, 1, "reaper", "reaper SteamLaunch AppId=12345 -- game", 'S'),
		proc(101, 100, "bwrap", "wrapper", 'S'),
		proc(102, 101, "bwrap", "adverb", 'S'),
		proc(103, 102, "game.exe", "game.exe", 'S'),
	}
	if found := gameProcessesOf(processes, 4072721196, usableExceptZombies); found != nil {
		t.Fatalf("found %v for another game", pids(found))
	}
	// a wrapper that only carries the same text is not a reaper
	processes = []*Process{proc(5, 1, "steam-runtime-launch-client", testReaperCommand, 'S')}
	if found := gameProcessesOf(processes, 4072721196, usableExceptZombies); found != nil {
		t.Fatalf("found %v from a wrapper", pids(found))
	}
}
