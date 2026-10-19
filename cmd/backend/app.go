package main

import (
	"errors"
	"sync"
	"time"

	"github.com/kayon/memscan"
	"github.com/kayon/memscan/deck"
	"github.com/kayon/memscan/scanner"
)

const (
	MinResultsThreshold = 10
	MaxResultsThreshold = 100
)

// App is what the plugin talks to. It scans every type a typed value can be
// searched in at once (a memscan.Session). The single-type functions of the
// previous versions (FirstScan, NextScan...) are kept on top of it, for a
// session that searches one type only.
type App struct {
	// mu serializes the exported calls: they can come from different threads
	mu sync.Mutex

	session *memscan.Session
	game    *deck.Process
	// the value of each type, as typed last: it gives the display of the results
	values map[scanner.Type]*scanner.Value
	option scanner.Option
	// the type of the single-type API
	legacyType scanner.Type

	renderResultsThreshold int
}

// TypeResults is the state of one type after a scan.
type TypeResults struct {
	Type  int
	Name  string
	Count int
	// ok, parked (too many results to list), truncated (too many to keep),
	// empty (no result left) or dropped (the value does not fit the type)
	Status string
	// the addresses and values, only for a type whose status is ok and whose
	// results are few enough to be listed
	List [][2]string
}

// MultiResults is the state of all the searched types after a scan.
type MultiResults struct {
	Types   []TypeResults
	Round   uint
	Time    string
	CanUndo bool
}

func (app *App) SetRenderResultsThreshold(value int) {
	if value >= MinResultsThreshold && value <= MaxResultsThreshold && value != app.renderResultsThreshold {
		app.renderResultsThreshold = value
	}
}

func (app *App) Clear() {
	app.ResetScan()
}

func (app *App) ResetScan() {
	app.session.Reset()
	app.values = nil
}

func (app *App) GetGameProcesses(appID int64) []*deck.Process {
	return deck.EnumGameProcesses(appID)
}

func (app *App) SelectGameProcess(appID int64, pid int) *deck.Process {
	if proc, _ := deck.NewProcess(pid); proc != nil {
		proc.AppID = appID
		app.game = proc
	}
	return app.game
}

func (app *App) AutoSelectGameProcess(appID int64) *deck.Process {
	if app.game == nil || (app.game.AppID != appID || !app.game.Alive()) {
		app.game = deck.FindGameWithAppID(appID)
	}
	return app.game
}

// typesBit is the bit of a type in the mask given to FirstScanMulti.
func typesBit(typ scanner.Type) uint {
	return 1 << uint(typ)
}

// FirstScanMulti searches the typed value in every type that can hold it, or
// only in the types of typesMask (one bit per scanner.Type) when it is not 0.
// GameProcess must have been selected before.
func (app *App) FirstScanMulti(appID int64, value string, option scanner.Option, emulatorMode bool, typesMask uint) *MultiResults {
	app.AutoSelectGameProcess(appID)
	if app.game == nil {
		return nil
	}

	var candidates []*scanner.Value
	for _, v := range scanner.ParseCandidates(value, option, false) {
		if typesMask == 0 || typesMask&typesBit(v.Type()) != 0 {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	if err := app.session.Open(app.game); err != nil {
		return nil
	}
	dur, err := app.session.FirstScan(candidates, emulatorMode)
	if err != nil {
		return nil
	}

	app.option = option
	app.values = make(map[scanner.Type]*scanner.Value, len(candidates))
	for _, v := range candidates {
		app.values[v.Type()] = v
	}
	return app.render(dur)
}

// NextScanMulti keeps, in each type, the results that now hold the value.
// A type the value does not fit is dropped, a type without result left is
// emptied; UndoScanMulti brings them back.
func (app *App) NextScanMulti(value string) *MultiResults {
	if app.game == nil || app.values == nil || app.session.Rounds() < 1 {
		return nil
	}
	candidates := scanner.ParseCandidates(value, app.option, true)
	if len(candidates) == 0 {
		// not a number: nothing changes
		return app.render(0)
	}
	return app.nextScan(candidates)
}

func (app *App) nextScan(candidates []*scanner.Value) *MultiResults {
	dur, err := app.session.NextScan(candidates)
	if err != nil {
		if errors.Is(err, memscan.ErrNoActiveType) {
			return app.render(0)
		}
		return nil
	}
	for _, v := range candidates {
		app.values[v.Type()] = v
	}
	return app.render(dur)
}

func (app *App) UndoScanMulti() *MultiResults {
	if app.game == nil || app.values == nil {
		return nil
	}
	if app.session.UndoScan() {
		return app.render(0)
	}
	return nil
}

// ChangeValuesMulti writes the value over the results of one type designated
// by their position in the list (all of them when indexes is empty).
func (app *App) ChangeValuesMulti(typ scanner.Type, value string, indexes []int) *MultiResults {
	if app.game == nil || app.values == nil {
		return nil
	}
	if n := app.count(typ); n == 0 || n > app.renderResultsThreshold {
		return nil
	}
	v := scanner.ParseAs(value, typ, app.option, true)
	if v == nil {
		return nil
	}
	if err := app.session.ChangeValues(typ, v, indexes); err != nil {
		return nil
	}
	app.values[typ] = v
	return app.render(0)
}

// RefreshValuesMulti reads the current values of the listed results again.
func (app *App) RefreshValuesMulti() *MultiResults {
	if app.game == nil || app.values == nil {
		return nil
	}
	return app.render(0)
}

func (app *App) count(typ scanner.Type) int {
	if m := app.session.Scan(typ); m != nil {
		return m.Count()
	}
	return 0
}

func (app *App) render(dur time.Duration) *MultiResults {
	results := &MultiResults{
		Round:   app.session.Rounds(),
		CanUndo: app.session.CanUndo(),
	}
	if dur > 0 {
		results.Time = dur.String()
	}
	for _, info := range app.session.Infos() {
		tr := TypeResults{
			Type:   int(info.Type),
			Name:   info.Type.String(),
			Count:  info.Count,
			Status: info.Status.String(),
		}
		if info.Status == memscan.StatusOK && info.Count > 0 && info.Count <= app.renderResultsThreshold {
			if v := app.values[info.Type]; v != nil {
				tr.List, _ = app.session.Render(info.Type, v)
			}
		}
		results.Types = append(results.Types, tr)
	}
	return results
}

// ---------------------------------------------------------------------------
// Single-type API of the previous versions, on top of the multi-type one. It
// is meant for a session that searches one type: FirstScan fixes it.

type Results struct {
	Count   int
	List    [][2]string
	Round   uint
	Time    string
	CanUndo bool
}

// single extracts the legacy type from a multi-type result.
func (app *App) single(multi *MultiResults) *Results {
	if multi == nil {
		return nil
	}
	results := &Results{Round: multi.Round, Time: multi.Time, CanUndo: multi.CanUndo}
	for _, tr := range multi.Types {
		if scanner.Type(tr.Type) == app.legacyType {
			results.Count, results.List = tr.Count, tr.List
		}
	}
	return results
}

// FirstScan searches the value in one type. GameProcess must have been
// selected before.
func (app *App) FirstScan(appID int64, value string, valueType scanner.Type, option scanner.Option, emulatorMode bool) *Results {
	multi := app.FirstScanMulti(appID, value, option, emulatorMode, typesBit(valueType))
	if multi == nil {
		return nil
	}
	app.legacyType = valueType
	return app.single(multi)
}

func (app *App) NextScan(value string) *Results {
	if app.game == nil || app.values[app.legacyType] == nil || app.session.Rounds() < 1 {
		return nil
	}
	v := scanner.ParseAs(value, app.legacyType, app.option, true)
	if v == nil {
		// not a number, or one the type cannot hold: nothing changes
		return app.single(app.render(0))
	}
	return app.single(app.nextScan([]*scanner.Value{v}))
}

func (app *App) UndoScan() *Results {
	if app.game == nil || app.values[app.legacyType] == nil {
		return nil
	}
	return app.single(app.UndoScanMulti())
}

func (app *App) ChangeValues(value string, indexes []int) *Results {
	if app.game == nil || app.values[app.legacyType] == nil {
		return nil
	}
	return app.single(app.ChangeValuesMulti(app.legacyType, value, indexes))
}

func (app *App) RefreshValues() *Results {
	if app.game == nil || app.values[app.legacyType] == nil {
		return nil
	}
	if n := app.count(app.legacyType); n == 0 || n > app.renderResultsThreshold {
		return nil
	}
	return app.single(app.render(0))
}
