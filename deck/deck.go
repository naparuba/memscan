package deck

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// DeckUID from username "deck"
const DeckUID = 1000

//const ReaperPathPrefix = "/home/deck/.local/share/Steam/"

var selfPID int

func init() {
	selfPID = os.Getpid()
}

var gameProcessExclude = []*regexp.Regexp{
	regexp.MustCompile(`^/usr/`),
	regexp.MustCompile(`^Xwayland`),
	//regexp.MustCompile(`home[/\\]deck[/\\]\.local[/\\]`),
	regexp.MustCompile(`^\\\\`),
	regexp.MustCompile(`^[cC]:`),
	regexp.MustCompile(`/home/deck/homebrew/plugins/`),
	regexp.MustCompile(`/home/deck/\.local/share/Steam/ubuntu`),
	regexp.MustCompile(`/home/deck/\.local/share/Steam/steamapps/common/(GE-)?Proton`),
	regexp.MustCompile(`/home/deck/\.local/share/Steam/compatibilitytools\.d/`),
	regexp.MustCompile(`/home/deck/\.local/share/Steam/steamapps/common/Steam`),
}

var procNameInvalid = map[string]bool{
	"Main":       true,
	"MainThread": true,
}

func findSteamLaunchGameProcesses(processes []*Process, reaperPID int) []*Process {
	wrapIndex := slices.IndexFunc(processes, func(proc *Process) bool {
		return proc.PPID == reaperPID
	})

	if wrapIndex == -1 {
		return nil
	}
	wrap := processes[wrapIndex]

	adverbIndex := slices.IndexFunc(processes[wrapIndex:], func(proc *Process) bool {
		return proc.PPID == wrap.PID
	})
	if adverbIndex == -1 {
		return nil
	}
	adverbIndex += wrapIndex
	adverb := processes[adverbIndex]

	var children = make([]*Process, 0, 32)

	for _, proc := range processes[adverbIndex:] {
		if proc.PPID == adverb.PID {
			children = append(children, proc)
		}
	}

	if len(children) == 0 {
		return nil
	}

	slices.SortFunc(children, func(a, b *Process) int {
		return b.GetIdentityScore() - a.GetIdentityScore()
	})

	return children
}

func FindGameWithAppID(appID int64) *Process {
	processes := EnumGameProcesses(appID)
	if len(processes) > 0 {
		return processes[0]
	}
	return nil
}

// FindGameWithInstanceID
// Deprecated
func FindGameWithInstanceID(instanceID int) *Process {
	processes, err := EnumDeckProcesses()
	if err != nil {
		return nil
	}
	slices.SortFunc(processes, func(a, b *Process) int {
		return a.PID - b.PID
	})
	processes = findSteamLaunchGameProcesses(processes, instanceID)
	if len(processes) > 0 {
		return processes[0]
	}
	return nil
}

func EnumGameProcesses(args ...int64) []*Process {
	processes, err := EnumDeckProcesses()
	if err != nil {
		return nil
	}

	var appID int64
	if len(args) > 0 && args[0] > 0 {
		appID = args[0]
	}

	return gameProcessesOf(processes, appID, isGameProcess)
}

// isGameProcess: a process that can be the game, not a zombie left by a game that ended nor
// one of the helpers of the launch.
func isGameProcess(proc *Process) bool {
	return proc.State != 'Z' && proc.GetIdentityScore() > 0
}

// gameProcessesOf finds the processes of the game among all the processes, with the launch
// "reaper" of Steam as the starting point. Steam can leave several of them for the same game
// (the game was started again, and the reaper of the first launch is still there, with a
// zombie below), so the newest one is looked at first, and the first that leads to a process
// that usable accepts is the one.
func gameProcessesOf(processes []*Process, appID int64, usable func(*Process) bool) []*Process {
	slices.SortFunc(processes, func(a, b *Process) int {
		return a.PID - b.PID
	})

	pattern := `SteamLaunch AppId=`
	if appID > 0 {
		pattern += fmt.Sprintf("%d", appID)
	}

	// The SteamLaunch "reaper" process is the one actually named "reaper".
	// Wrapper processes (steam-runtime-launch-client, steam-runtime-supervisor)
	// embed the same "SteamLaunch AppId=..." string in their command line and
	// would otherwise be matched first (they have lower PIDs) and have no
	// children, making game detection fail on newer Steam runtimes.
	for i := len(processes) - 1; i >= 0; i-- {
		reaper := processes[i]
		if reaper.Comm != "reaper" || !strings.Contains(reaper.Command, pattern) {
			continue
		}
		found := findSteamLaunchGameProcesses(processes[i:], reaper.PID)
		found = slices.DeleteFunc(found, func(proc *Process) bool {
			return !usable(proc)
		})
		if len(found) == 0 {
			continue
		}
		if appID > 0 {
			for _, proc := range found {
				proc.AppID = appID
			}
		}
		return found
	}
	return nil
}

// LegacyEnumGameProcesses
// Deprecated
func LegacyEnumGameProcesses() []*Process {
	processes, err := EnumDeckProcesses()
	if err != nil {
		return nil
	}

	var candidates []*Process
	for _, p := range processes {
		if p.GetIdentityScore() > 0 {
			var excluded bool
			for _, reg := range gameProcessExclude {
				if reg.MatchString(p.Command) {
					excluded = true
					break
				}
			}
			if !excluded {
				candidates = append(candidates, p)
			}
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		scoreI := candidates[i].GetIdentityScore()
		scoreJ := candidates[j].GetIdentityScore()

		if scoreI != scoreJ {
			return scoreI > scoreJ
		}
		return candidates[i].RSS > candidates[j].RSS
	})

	return candidates
}
