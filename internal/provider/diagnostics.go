package provider

import "fmt"

// Warning is a non-fatal problem encountered while scanning.
type Warning struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Msg  string `json:"msg"`
}

// Diagnostics aggregates non-fatal scanner problems. Warnings are capped;
// Dropped counts how many were discarded beyond the cap.
type Diagnostics struct {
	ParseErrors  int            `json:"parseErrors"`
	UnknownTypes map[string]int `json:"unknownTypes"`
	Warnings     []Warning      `json:"warnings"` // capped at 100; Dropped counts the rest
	Dropped      int            `json:"dropped"`
}

// WarnCap is the maximum number of retained warnings.
const WarnCap = 100

// Warn records a warning, dropping it (and counting it) once the cap is hit.
func (d *Diagnostics) Warn(path string, line int, format string, args ...any) {
	if d.Warnings == nil {
		d.Warnings = make([]Warning, 0, WarnCap)
	}
	if len(d.Warnings) >= WarnCap {
		d.Dropped++
		return
	}
	d.Warnings = append(d.Warnings, Warning{
		Path: path,
		Line: line,
		Msg:  fmt.Sprintf(format, args...),
	})
}

// Unknown counts an unrecognized record type by name.
func (d *Diagnostics) Unknown(kind string) {
	if d.UnknownTypes == nil {
		d.UnknownTypes = make(map[string]int)
	}
	d.UnknownTypes[kind]++
}

// Merge folds another Diagnostics into d. Merged warnings fill the cap in
// order; anything beyond the cap (in o or from overflow) counts as Dropped.
func (d *Diagnostics) Merge(o Diagnostics) {
	d.ParseErrors += o.ParseErrors
	if len(o.UnknownTypes) > 0 {
		if d.UnknownTypes == nil {
			d.UnknownTypes = make(map[string]int, len(o.UnknownTypes))
		}
		for k, v := range o.UnknownTypes {
			d.UnknownTypes[k] += v
		}
	}
	room := WarnCap - len(d.Warnings)
	keep := o.Warnings
	if room < len(keep) {
		keep = keep[:max(room, 0)]
	}
	d.Warnings = append(d.Warnings, keep...)
	d.Dropped += o.Dropped + len(o.Warnings) - len(keep)
}
