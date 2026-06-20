package defs

import (
	"github.com/terapps/gonveyor"
	"github.com/terapps/gonveyor/blueprint"
)

type CutSteelInput struct {
	OrderID   string
	SheetSize string
}

type CutSteelOutput struct {
	SheetID string
}

type DrillHolesInput struct {
	SheetID string
}

type DrillHolesOutput struct {
	SheetID   string
	HoleCount int
}

type MillSurfaceInput struct {
	SheetID string
}

type MillSurfaceOutput struct {
	SheetID   string
	Roughness float64
}

type BendFrameInput struct {
	SheetID string
}

type BendFrameOutput struct {
	FrameID string
}

type WeldAssemblyInput struct {
	SheetID   string
	HoleCount int
	Roughness float64
	FrameID   string
}

type WeldAssemblyOutput struct {
	AssemblyID string
}

type CoatAssemblyInput struct {
	AssemblyID string
}

type CoatAssemblyOutput struct {
	AssemblyID  string
	CoatingType string
}

type InspectInput struct {
	AssemblyID string
}

type InspectOutput struct {
	AssemblyID string
	Passed     bool
}

var CutSteel = blueprint.Define[CutSteelInput, CutSteelOutput]("cut_steel")
var DrillHoles = blueprint.Define[DrillHolesInput, DrillHolesOutput]("drill_holes")
var MillSurface = blueprint.Define[MillSurfaceInput, MillSurfaceOutput]("mill_surface")
var BendFrame = blueprint.Define[BendFrameInput, BendFrameOutput]("bend_frame")
var WeldAssembly = blueprint.Define[WeldAssemblyInput, WeldAssemblyOutput]("weld_assembly")
var CoatAssembly = blueprint.Define[CoatAssemblyInput, CoatAssemblyOutput]("coat_assembly")
var Inspect = blueprint.Define[InspectInput, InspectOutput]("inspect")

//	┌──> drill_holes ───┐
//
// cut_steel ──────┼──> mill_surface ──┼──> weld_assembly ──> coat_assembly ──> inspect
//
//	└──> bend_frame ────┘
var SteelFrameDAG = blueprint.New("steel_frame_dag",
	CutSteel,
	blueprint.Wire(DrillHoles,
		gonveyor.Intake(CutSteel, func(o CutSteelOutput, in *DrillHolesInput) {
			in.SheetID = o.SheetID
		}),
	),
	blueprint.Wire(MillSurface,
		gonveyor.Intake(CutSteel, func(o CutSteelOutput, in *MillSurfaceInput) {
			in.SheetID = o.SheetID
		}),
	),
	blueprint.Wire(BendFrame,
		gonveyor.Intake(CutSteel, func(o CutSteelOutput, in *BendFrameInput) {
			in.SheetID = o.SheetID
		}),
	),
	blueprint.Wire(WeldAssembly,
		gonveyor.Intake(DrillHoles, func(o DrillHolesOutput, in *WeldAssemblyInput) {
			in.SheetID = o.SheetID
			in.HoleCount = o.HoleCount
		}),
		gonveyor.Intake(MillSurface, func(o MillSurfaceOutput, in *WeldAssemblyInput) {
			in.Roughness = o.Roughness
		}),
		gonveyor.Intake(BendFrame, func(o BendFrameOutput, in *WeldAssemblyInput) {
			in.FrameID = o.FrameID
		}),
	),
	blueprint.Wire(CoatAssembly,
		gonveyor.Intake(WeldAssembly, func(o WeldAssemblyOutput, in *CoatAssemblyInput) {
			in.AssemblyID = o.AssemblyID
		}),
	),
	blueprint.Wire(Inspect,
		gonveyor.Intake(CoatAssembly, func(o CoatAssemblyOutput, in *InspectInput) {
			in.AssemblyID = o.AssemblyID
		}),
	),
)
