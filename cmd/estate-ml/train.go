package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tdar200/estate-games/internal/valuenet"
)

// train: fit the value network on recorded positions.
//
// Positions are split by game (1 game in -val-every goes to validation, so
// no game is on both sides). Adam with a cosine learning-rate decay; every
// epoch is checkpointed (net/<out>/ckpt.bin, adam.bin, epoch.json) and a
// restart resumes after the last finished epoch. The epoch with the lowest
// validation log loss is kept as net/<out>/best.bin, with its metrics
// (log loss, accuracy, Brier score, calibration, by game stage and player
// count) in metrics.json.

type trainMeta struct {
	Epoch    int     `json:"epoch"`
	BestLoss float64 `json:"bestLoss"`
	BestEp   int     `json:"bestEpoch"`
}

func cmdTrain(args []string) {
	fs := flag.NewFlagSet("train", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	sets := fs.String("sets", "gen1", "comma-separated datasets (data/<set>)")
	out := fs.String("out", "gen1", "output name (net/<out>)")
	epochs := fs.Int("epochs", 12, "epochs")
	h1 := fs.Int("h1", 128, "first hidden layer")
	h2 := fs.Int("h2", 64, "second hidden layer")
	batch := fs.Int("batch", 1024, "minibatch size")
	lr := fs.Float64("lr", 2e-3, "initial learning rate")
	lrFinal := fs.Float64("lr-final", 5e-5, "final learning rate (cosine decay)")
	wd := fs.Float64("wd", 1e-5, "decoupled weight decay")
	workers := fs.Int("workers", 24, "goroutines")
	valEvery := fs.Int("val-every", 20, "one game in N is held out for validation")
	seed := fs.Uint64("seed", 11, "seed")
	initPath := fs.String("init", "", "start from this model file instead of random weights")
	dropout := fs.Float64("dropout", 0, "hidden-unit dropout rate")
	fs.Parse(args)

	outDir := filepath.Join(*dir, "net", *out)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fatalf("train: %v", err)
	}
	rep := newReporter(*dir, "train", "train-"+*out)
	defer rep.close()
	rep.set("Training value net · loading data", "Reading positions from "+*sets, nil)
	all, err := loadRecords(*dir, strings.Split(*sets, ","))
	if err != nil {
		fatalf("train: %v", err)
	}
	var trainSet, valSet []valuenet.Record
	for _, r := range all {
		if mixSeed(uint64(r.Game), 0xa1)%uint64(*valEvery) == 0 {
			valSet = append(valSet, r)
		} else {
			trainSet = append(trainSet, r)
		}
	}
	all = nil
	logf("train: %d training positions, %d validation positions", len(trainSet), len(valSet))
	if len(trainSet) == 0 || len(valSet) == 0 {
		fatalf("train: no data")
	}

	var net *valuenet.Net
	meta := trainMeta{BestLoss: math.Inf(1)}
	ckpt := filepath.Join(outDir, "ckpt.bin")
	resumed := false
	if readJSON(filepath.Join(outDir, "epoch.json"), &meta) == nil && meta.Epoch > 0 {
		if net, err = valuenet.Load(ckpt); err != nil {
			fatalf("train: checkpoint: %v", err)
		}
		resumed = true
	} else if *initPath != "" {
		if net, err = valuenet.Load(*initPath); err != nil {
			fatalf("train: %v", err)
		}
	} else {
		net = valuenet.NewNet(valuenet.NumFeatures, *h1, *h2, *seed)
	}
	tr := valuenet.NewTrainer(net, valuenet.TrainConfig{Batch: *batch, LR: *lr, WeightDecay: *wd, Workers: *workers, Dropout: *dropout})
	if resumed {
		if err := tr.LoadState(filepath.Join(outDir, "adam.bin")); err != nil {
			fatalf("train: optimiser state: %v", err)
		}
		event(*dir, "info", "Value net training (%s) resumed after epoch %d", *out, meta.Epoch)
	} else {
		event(*dir, "info", "Value net training (%s) started: %d training and %d validation positions, %d-%d-%d-1 network",
			*out, len(trainSet), len(valSet), valuenet.NumFeatures, net.H1, net.H2)
		base := valuenet.Evaluate(net, valSet, *workers)
		logf("train: untrained val log loss %.4f (uniform %.4f)", base.LogLoss, base.Uniform)
	}
	rng := rand.New(rand.NewPCG(*seed, uint64(meta.Epoch)))
	for epoch := meta.Epoch; epoch < *epochs; epoch++ {
		rate := *lrFinal + 0.5*(*lr-*lrFinal)*(1+math.Cos(math.Pi*float64(epoch)/float64(*epochs)))
		start := time.Now()
		label := fmt.Sprintf("Training value net · epoch %d/%d", epoch+1, *epochs)
		rep.set(label, fmt.Sprintf("%d positions per epoch, learning rate %.2g", len(trainSet), rate), nil)
		perm := valuenet.Shuffle(len(trainSet), rng)
		lastReport := time.Now()
		trainLoss := tr.TrainEpoch(trainSet, perm, rate, func(done int) {
			if time.Since(lastReport) > 5*time.Second {
				lastReport = time.Now()
				frac := float64(done) / float64(len(trainSet))
				eta := time.Since(start).Seconds() / frac * (1 - frac + float64(*epochs-epoch-1))
				rep.set(label, fmt.Sprintf("%.0f%% of epoch %d, learning rate %.2g", 100*frac, epoch+1, rate), etaPtr(eta))
			}
		})
		m := valuenet.Evaluate(net, valSet, *workers)
		logf("epoch %d: train loss %.4f, val loss %.4f (uniform %.4f), accuracy %.4f, brier %.4f, lr %.2g (%s)",
			epoch+1, trainLoss, m.LogLoss, m.Uniform, m.Accuracy, m.Brier, rate, time.Since(start).Round(time.Second))
		_ = appendJSONL(filepath.Join(*dir, "status", "train.jsonl"), map[string]any{
			"epoch": epoch + 1, "trainLoss": round4(trainLoss), "valLoss": round4(m.LogLoss), "valAccuracy": round4(m.Accuracy),
			"brier": round4(m.Brier), "lr": rate, "at": nowMS(), "net": *out, "uniformLoss": round4(m.Uniform),
		})
		if err := net.Save(filepath.Join(outDir, fmt.Sprintf("model-ep%02d.bin", epoch+1))); err != nil {
			fatalf("train: %v", err)
		}
		improved := m.LogLoss < meta.BestLoss
		if improved {
			meta.BestLoss, meta.BestEp = m.LogLoss, epoch+1
			if err := net.Save(filepath.Join(outDir, "best.bin")); err != nil {
				fatalf("train: %v", err)
			}
			_ = writeJSONAtomic(filepath.Join(outDir, "metrics.json"), map[string]any{"epoch": epoch + 1, "validation": m,
				"trainPositions": len(trainSet), "valPositions": len(valSet), "sets": *sets})
		}
		event(*dir, "info", "Value net epoch %d: val loss %.3f (chance %.3f), winner picked %.1f%%, Brier %.3f%s",
			epoch+1, m.LogLoss, m.Uniform, 100*m.Accuracy, m.Brier, map[bool]string{true: " (best so far)", false: ""}[improved])
		meta.Epoch = epoch + 1
		if err := net.Save(ckpt); err != nil {
			fatalf("train: %v", err)
		}
		if err := tr.SaveState(filepath.Join(outDir, "adam.bin")); err != nil {
			fatalf("train: %v", err)
		}
		if err := writeJSONAtomic(filepath.Join(outDir, "epoch.json"), meta); err != nil {
			fatalf("train: %v", err)
		}
	}
	rep.set("Value net trained", fmt.Sprintf("Best validation log loss %.4f at epoch %d", meta.BestLoss, meta.BestEp), nil)
	event(*dir, "info", "Value net (%s) finished: best validation log loss %.3f at epoch %d", *out, meta.BestLoss, meta.BestEp)
}

// evalnet: metrics of one or more models on the validation games of the
// given datasets (the same split as train).
func cmdEvalNet(args []string) {
	fs := flag.NewFlagSet("evalnet", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "work directory")
	sets := fs.String("sets", "gen1", "comma-separated datasets")
	valEvery := fs.Int("val-every", 20, "one game in N is validation (as in train)")
	workers := fs.Int("workers", 18, "goroutines")
	out := fs.String("out", "", "write the metrics of the first model as JSON here")
	fs.Parse(args)
	all, err := loadRecords(*dir, strings.Split(*sets, ","))
	if err != nil {
		fatalf("evalnet: %v", err)
	}
	var val []valuenet.Record
	for _, r := range all {
		if mixSeed(uint64(r.Game), 0xa1)%uint64(*valEvery) == 0 {
			val = append(val, r)
		}
	}
	all = nil
	logf("evalnet: %d validation positions from %s", len(val), *sets)
	for i, path := range fs.Args() {
		net, err := valuenet.Load(path)
		if err != nil {
			fatalf("evalnet: %v", err)
		}
		m := valuenet.Evaluate(net, val, *workers)
		fmt.Printf("%s: log loss %.4f (uniform %.4f), accuracy %.4f, brier %.4f\n", path, m.LogLoss, m.Uniform, m.Accuracy, m.Brier)
		for _, s := range m.ByStage {
			fmt.Printf("   %-14s n=%-8d log loss %.4f (uniform %.4f) accuracy %.4f\n", s.Name, s.N, s.LogLoss, s.Uniform, s.Accuracy)
		}
		for _, s := range m.ByPlayers {
			fmt.Printf("   %-14s n=%-8d log loss %.4f (uniform %.4f) accuracy %.4f\n", s.Name, s.N, s.LogLoss, s.Uniform, s.Accuracy)
		}
		for _, c := range m.Calibration {
			fmt.Printf("   p %.1f-%.1f: n=%-8d predicted %.3f observed %.3f\n", c.Lo, c.Hi, c.Count, c.Predicted, c.Observed)
		}
		if i == 0 && *out != "" {
			_ = writeJSONAtomic(*out, map[string]any{"model": path, "sets": *sets, "validation": m})
		}
	}
}
