package niimbot

import "strings"

// Task identifies the print-command sequence a model expects. Different
// generations of firmware use different PrintStart/SetPageSize payloads and
// different completion signalling (see docs/niimbot.md §5).
type Task int

// Print tasks.
const (
	// TaskB1: 7-byte PrintStart, 6-byte SetPageSize (rows, cols, copies),
	// completion by polling PrintStatus. B1, B21_C2B, D110_M, N1, D101, M2_H.
	TaskB1 Task = iota
	// TaskB21V1: 1-byte PrintStart, 4-byte SetPageSize, one page per copy,
	// check-line marker every 200 rows, completion by polling PrintEnd. B21.
	TaskB21V1
	// TaskD110: 1-byte PrintStart, PrintClear + 4-byte SetPageSize +
	// PrintQuantity per page, completion by polling PrintStatus. B21S, D110.
	TaskD110
)

// String names the task.
func (t Task) String() string {
	switch t {
	case TaskB1:
		return "B1"
	case TaskB21V1:
		return "B21_V1"
	case TaskD110:
		return "D110"
	}
	return "unknown"
}

// Model describes a printer model's geometry and protocol variant.
type Model struct {
	// Name is the marketing name, e.g. "B1", "B21".
	Name string
	// DeviceTypes are the values InfoDeviceType reports for this model.
	DeviceTypes []int
	// DPI is the print resolution (dots per inch).
	DPI int
	// HeadPixels is the print-head width in dots (max image columns).
	HeadPixels int
	// Task is the print sequence variant.
	Task Task
	// DensityMin/Max bound CmdSetDensity; DensityDefault is used when a job
	// does not specify one.
	DensityMin, DensityMax, DensityDefault int
	// LabelTypes the model accepts.
	LabelTypes []LabelType
	// Supported reports whether this bridge has been verified against the
	// model. Unverified models are still driven best-effort.
	Verified bool
}

// Known models, keyed by device type. Geometry from niimbluelib's printer
// model library; task mapping from its modelPrintTasks table.
var models = []Model{
	{Name: "B1", DeviceTypes: []int{4096}, DPI: 203, HeadPixels: 384, Task: TaskB1,
		DensityMin: 1, DensityMax: 5, DensityDefault: 3,
		LabelTypes: []LabelType{LabelGap, LabelBlackMark, LabelTransparent}, Verified: true},
	{Name: "B21", DeviceTypes: []int{768}, DPI: 203, HeadPixels: 384, Task: TaskB21V1,
		DensityMin: 1, DensityMax: 5, DensityDefault: 3,
		LabelTypes: []LabelType{LabelGap, LabelBlackMark, LabelContinuous, LabelTransparent}},
	{Name: "B21_C2B", DeviceTypes: []int{771, 775}, DPI: 203, HeadPixels: 384, Task: TaskB1,
		DensityMin: 1, DensityMax: 5, DensityDefault: 3,
		LabelTypes: []LabelType{LabelGap, LabelContinuous, LabelTransparent, LabelBlackMark}},
	{Name: "B21_L2B", DeviceTypes: []int{769}, DPI: 203, HeadPixels: 384, Task: TaskB21V1,
		DensityMin: 1, DensityMax: 5, DensityDefault: 3,
		LabelTypes: []LabelType{LabelGap, LabelBlackMark, LabelTransparent}},
	{Name: "B21S", DeviceTypes: []int{777}, DPI: 203, HeadPixels: 384, Task: TaskD110,
		DensityMin: 1, DensityMax: 5, DensityDefault: 3,
		LabelTypes: []LabelType{LabelGap, LabelBlackMark, LabelContinuous, LabelTransparent}},
	{Name: "B21S_C2B", DeviceTypes: []int{776}, DPI: 203, HeadPixels: 384, Task: TaskD110,
		DensityMin: 1, DensityMax: 5, DensityDefault: 3,
		LabelTypes: []LabelType{LabelGap, LabelBlackMark, LabelTransparent}},
	{Name: "B18", DeviceTypes: []int{3584}, DPI: 203, HeadPixels: 96, Task: TaskB1,
		DensityMin: 1, DensityMax: 3, DensityDefault: 2,
		LabelTypes: []LabelType{LabelGap, LabelTransparent, LabelBlackMarkGap, LabelContinuous}},
}

// ModelByDeviceType looks a model up by the value InfoDeviceType reported.
func ModelByDeviceType(dt int) (Model, bool) {
	for _, m := range models {
		for _, d := range m.DeviceTypes {
			if d == dt {
				return m, true
			}
		}
	}
	return Model{}, false
}

// ModelByName looks a model up by name, case-insensitively ("b1", "B21").
func ModelByName(name string) (Model, bool) {
	for _, m := range models {
		if strings.EqualFold(m.Name, name) {
			return m, true
		}
	}
	return Model{}, false
}

// Models returns the known model table.
func Models() []Model { return append([]Model(nil), models...) }

// DefaultModel is assumed when the device type is unknown: the B1 task family
// is what most current NIIMBOT firmware speaks.
var DefaultModel = models[0]

// ModelFromAdvertisedName guesses the model from a Bluetooth name such as
// "B1-I711131967" or "B21-G3A12345". The device type read after connecting is
// authoritative; this is only for listing before connecting.
func ModelFromAdvertisedName(name string) (Model, bool) {
	up := strings.ToUpper(name)
	best, found := Model{}, false
	for _, m := range models {
		prefix := strings.ToUpper(m.Name)
		if strings.HasPrefix(up, prefix+"-") || strings.HasPrefix(up, prefix+"_") || up == prefix {
			if !found || len(m.Name) > len(best.Name) {
				best, found = m, true
			}
		}
	}
	return best, found
}

// LooksLikeNiimbot reports whether a Bluetooth advertised name matches a known
// NIIMBOT naming pattern.
func LooksLikeNiimbot(name string) bool {
	_, ok := ModelFromAdvertisedName(name)
	return ok
}
