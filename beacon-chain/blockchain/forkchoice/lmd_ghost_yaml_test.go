package forkchoice

import (
	"bytes"
	"context"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"testing"

	ethpb "github.com/prysmaticlabs/ethereumapis/eth/v1alpha1"
	"github.com/prysmaticlabs/go-ssz"
	"github.com/prysmaticlabs/prysm/beacon-chain/cache"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	testDB "github.com/prysmaticlabs/prysm/beacon-chain/db/testing"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"gopkg.in/yaml.v2"
)

type Config struct {
	TestCases []struct {
		Blocks []struct {
			ID     string `yaml:"id"`
			Parent string `yaml:"parent"`
		} `yaml:"blocks"`
		Weights map[string]int `yaml:"weights"`
		Head    string         `yaml:"head"`
	} `yaml:"test_cases"`
}

func TestGetHeadFromYaml(t *testing.T) {
	helpers.ClearCache()
	ctx := context.Background()
	filename, _ := filepath.Abs("./lmd_ghost_test.yaml")
	yamlFile, err := ioutil.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var c *Config
	err = yaml.Unmarshal(yamlFile, &c)

	params.UseMainnetConfig()

	for _, test := range c.TestCases {
		db := testDB.SetupDB(t)
		defer testDB.TeardownDB(t, db)

		blksRoot := make(map[int][]byte)
		// Construct block tree from yaml.
		for _, blk := range test.Blocks {
			// genesis block condition
			if blk.ID == blk.Parent {
				b := &ethpb.BeaconBlock{Slot: 0, ParentRoot: []byte{'g'}}
				if err := db.SaveBlock(ctx, &ethpb.SignedBeaconBlock{Block: b}); err != nil {
					t.Fatal(err)
				}
				root, err := ssz.HashTreeRoot(b)
				if err != nil {
					t.Fatal(err)
				}
				blksRoot[0] = root[:]
			} else {
				slot, err := strconv.Atoi(blk.ID[1:])
				if err != nil {
					t.Fatal(err)
				}
				parentSlot, err := strconv.Atoi(blk.Parent[1:])
				if err != nil {
					t.Fatal(err)
				}
				b := &ethpb.SignedBeaconBlock{Block: &ethpb.BeaconBlock{Slot: uint64(slot), ParentRoot: blksRoot[parentSlot]}}
				if err := db.SaveBlock(ctx, b); err != nil {
					t.Fatal(err)
				}
				root, err := ssz.HashTreeRoot(b.Block)
				if err != nil {
					t.Fatal(err)
				}
				blksRoot[slot] = root[:]
				if err := db.SaveState(ctx, &pb.BeaconState{}, root); err != nil {
					t.Fatal(err)
				}
			}
		}

		store := NewForkChoiceService(ctx, db)

		// Assign validator votes to the blocks as weights.
		count := 0
		for blk, votes := range test.Weights {
			slot, err := strconv.Atoi(blk[1:])
			if err != nil {
				t.Fatal(err)
			}
			max := count + votes
			for i := count; i < max; i++ {
				store.latestVoteMap[uint64(i)] = &pb.ValidatorLatestVote{Root: blksRoot[slot]}
				count++
			}
		}

		validators := make([]*ethpb.Validator, count)
		for i := 0; i < len(validators); i++ {
			validators[i] = &ethpb.Validator{ExitEpoch: 2, EffectiveBalance: 1e9}
		}

		s := &pb.BeaconState{Validators: validators, RandaoMixes: make([][]byte, params.BeaconConfig().EpochsPerHistoricalVector)}

		if err := store.db.SaveState(ctx, s, bytesutil.ToBytes32(blksRoot[0])); err != nil {
			t.Fatal(err)
		}
		if err := store.GenesisStore(ctx, &ethpb.Checkpoint{Root: blksRoot[0]}, &ethpb.Checkpoint{Root: blksRoot[0]}); err != nil {
			t.Fatal(err)
		}

		if err := store.checkpointState.AddCheckpointState(&cache.CheckpointState{
			Checkpoint: store.justifiedCheckpt,
			State:      s,
		}); err != nil {
			t.Fatal(err)
		}

		head, err := store.Head(ctx)
		if err != nil {
			t.Fatal(err)
		}

		headSlot, err := strconv.Atoi(test.Head[1:])
		if err != nil {
			t.Fatal(err)
		}
		wantedHead := blksRoot[headSlot]

		if !bytes.Equal(head, wantedHead) {
			t.Errorf("wanted root %#x, got root %#x", wantedHead, head)
		}

		testDB.TeardownDB(t, db)
	}
}
