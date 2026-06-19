package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/terapps/gonveyor/transport/amqp"
	gonveyor "github.com/terapps/gonveyor"
	"github.com/terapps/gonveyor/examples/factory/defs"
	"github.com/terapps/gonveyor/examples/shared"
	"github.com/terapps/gonveyor/store"
)

var workflows = map[string]func() (store.BlueprintManifest, error){
	"assembly_line": func() (store.BlueprintManifest, error) {
		return defs.AssemblyLine.Manifest(defs.DrillInput{OrderID: "order-1", PartCode: "DR-42"})
	},
	"assembly_line_split": func() (store.BlueprintManifest, error) {
		return defs.AssemblyLine.Manifest(
			defs.DrillInput{OrderID: "order-1", PartCode: "DR-42"},
			gonveyor.Split(defs.AssemblePart, 3),
		)
	},
	"metal_pipeline": func() (store.BlueprintManifest, error) {
		return defs.MetalPipeline.Manifest(defs.CutBlankInput{OrderID: "order-2", MetalType: "steel"})
	},
	"steel_frame_dag": func() (store.BlueprintManifest, error) {
		return defs.SteelFrameDAG.Manifest(defs.CutSteelInput{OrderID: "order-3", SheetSize: "1200x800"})
	},
}

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("usage: publisher <workflow> [n]\navailable: %v", keys(workflows))
	}

	name := os.Args[1]

	build, ok := workflows[name]
	if !ok {
		log.Fatalf("unknown workflow %q, available: %v", name, keys(workflows))
	}

	n := 1
	if len(os.Args) >= 3 {
		var err error
		if n, err = strconv.Atoi(os.Args[2]); err != nil || n < 1 {
			log.Fatalf("n must be a positive integer")
		}
	}

	ctx := context.Background()

	gc, cleanup, err := shared.BuildGonductor(shared.Config{
		QueueName: "gonveyor",
		QueueOpts: []amqp.QueueOption{amqp.WithDeadLetter("gonveyor.dlx")},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	for range n {
		manifest, err := build()
		if err != nil {
			log.Fatal(err)
		}

		if err := gc.Submit(ctx, manifest); err != nil {
			log.Fatal(err)
		}

		if err := gc.Dispatch(ctx, manifest.PendingTasks()); err != nil {
			log.Fatal(err)
		}

		log.Printf("blueprint %s submitted and dispatched", manifest.Blueprint.ID)
	}
}

func keys[K comparable, V any](m map[K]V) []K {
	ks := make([]K, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}

	return ks
}
