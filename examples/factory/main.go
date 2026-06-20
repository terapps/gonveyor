package main

import (
	"context"
	"log"

	"github.com/terapps/gonveyor/examples/factory/defs"
	"github.com/terapps/gonveyor/examples/shared"
	"github.com/terapps/gonveyor/transport/amqp"
)

func main() {
	ctx := context.Background()

	g, cleanup, err := shared.Build(shared.Config{
		QueueName: "gonveyor",
		QueueOpts: []amqp.QueueOption{amqp.WithDeadLetter("gonveyor.dlx")},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	g.RegisterBlueprint(defs.AssemblyLine)
	g.RegisterBlueprint(defs.MetalPipeline)
	g.RegisterBlueprint(defs.SteelFrameDAG)

	g.RegisterHandler(defs.DrillPart, shared.DebugHandler())
	g.RegisterHandler(defs.AssemblePart, shared.DebugHandler())
	g.RegisterHandler(defs.InspectAssembly, shared.DebugHandler())
	g.RegisterHandler(defs.CutBlank, shared.DebugHandler())
	g.RegisterHandler(defs.DrillBlank, shared.DebugHandler())
	g.RegisterHandler(defs.CoatBlank, shared.DebugHandler())
	g.RegisterHandler(defs.CutSteel, shared.DebugHandler())
	g.RegisterHandler(defs.DrillHoles, shared.DebugHandler())
	g.RegisterHandler(defs.MillSurface, shared.DebugHandler())
	g.RegisterHandler(defs.BendFrame, shared.DebugHandler())
	g.RegisterHandler(defs.WeldAssembly, shared.DebugHandler())
	g.RegisterHandler(defs.CoatAssembly, shared.DebugHandler())
	g.RegisterHandler(defs.Inspect, shared.DebugHandler())

	log.Println("factory worker listening...")

	if err := g.Listen(ctx); err != nil {
		log.Fatal(err)
	}
}
