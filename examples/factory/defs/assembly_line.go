package defs

import (
	"github.com/terapps/gonveyor"
	"github.com/terapps/gonveyor/blueprint"
)

type DrillInput struct {
	OrderID  string
	PartCode string
}

type DrillOutput struct {
	PartID string
}

type AssembleInput struct {
	PartID string
}

type AssembleOutput struct {
	SubAssemblyID string
}

type InspectAssemblyInput struct {
	PartID         string
	SubAssemblyIDs []string
}

type InspectAssemblyOutput struct {
	Passed bool
}

var DrillPart = blueprint.Define[DrillInput, DrillOutput]("drill_part")
var AssemblePart = blueprint.Define[AssembleInput, AssembleOutput]("assemble_part")
var InspectAssembly = blueprint.Define[InspectAssemblyInput, InspectAssemblyOutput]("inspect_assembly")

// drill_part ──> assemble_part ──> inspect_assembly
//
// With Split(AssemblePart, N) at manifest time:
//
// drill_part ──┬──> assemble_part/0 ──┐
//
//	├──> assemble_part/1 ──┼──> inspect_assembly
//	└──> assemble_part/N ──┘
var AssemblyLine = blueprint.New("assembly_line",
	DrillPart,
	blueprint.Wire(AssemblePart,
		gonveyor.Intake(DrillPart, func(o DrillOutput, in *AssembleInput) {
			in.PartID = o.PartID
		}),
	),
	blueprint.Wire(InspectAssembly,
		gonveyor.Intake(DrillPart, func(o DrillOutput, in *InspectAssemblyInput) {
			in.PartID = o.PartID
		}),
		gonveyor.Merge(AssemblePart, func(outputs []AssembleOutput, in *InspectAssemblyInput) {
			ids := make([]string, len(outputs))
			for i, o := range outputs {
				ids[i] = o.SubAssemblyID
			}
			in.SubAssemblyIDs = ids
		}),
	),
)
