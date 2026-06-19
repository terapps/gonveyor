package defs

import (
	"github.com/terapps/gonveyor"
	"github.com/terapps/gonveyor/blueprint"
)

type CutBlankInput struct {
	OrderID   string
	MetalType string
}

type CutBlankOutput struct {
	BlankID    string
	Dimensions string
}

type DrillBlankInput struct {
	BlankID    string
	Dimensions string
}

type DrillBlankOutput struct {
	BlankID   string
	HoleCount int
}

type CoatBlankInput struct {
	BlankID     string
	CoatingType string
}

type CoatBlankOutput struct {
	PartID string
}

var CutBlank = blueprint.Define[CutBlankInput, CutBlankOutput]("cut_blank")

var DrillBlank = blueprint.Define[DrillBlankInput, DrillBlankOutput]("drill_blank",
	gonveyor.Intake(CutBlank, func(o CutBlankOutput, in *DrillBlankInput) {
		in.BlankID = o.BlankID
		in.Dimensions = o.Dimensions
	}),
)

var CoatBlank = blueprint.Define[CoatBlankInput, CoatBlankOutput]("coat_blank",
	gonveyor.Intake(DrillBlank, func(o DrillBlankOutput, in *CoatBlankInput) {
		in.BlankID = o.BlankID
	}),
)

// cut_blank ──> drill_blank ──> coat_blank
var MetalPipeline = blueprint.New("metal_pipeline", CutBlank, DrillBlank, CoatBlank)
