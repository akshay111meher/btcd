// Copyright (c) 2013-2017 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcutil"
)

// TestHaveBlock tests the HaveBlock API to ensure proper functionality.
func TestHaveBlock(t *testing.T) {
	// Load up blocks such that there is a side chain.
	// (genesis block) -> 1 -> 2 -> 3 -> 4
	//                          \-> 3a
	testFiles := []string{
		"blk_0_to_4.dat.bz2",
		"blk_3A.dat.bz2",
	}

	var blocks []*btcutil.Block
	for _, file := range testFiles {
		blockTmp, err := loadBlocks(file)
		if err != nil {
			t.Errorf("Error loading file: %v\n", err)
			return
		}
		blocks = append(blocks, blockTmp...)
	}

	// Create a new database and chain instance to run tests against.
	chain, teardownFunc, err := chainSetup("haveblock",
		&chaincfg.MainNetParams)
	if err != nil {
		t.Errorf("Failed to setup chain instance: %v", err)
		return
	}
	defer teardownFunc()

	// Since we're not dealing with the real block chain, set the coinbase
	// maturity to 1.
	chain.TstSetCoinbaseMaturity(1)

	for i := 1; i < len(blocks); i++ {
		_, isOrphan, err := chain.ProcessBlock(blocks[i], BFNone)
		if err != nil {
			t.Errorf("ProcessBlock fail on block %v: %v\n", i, err)
			return
		}
		if isOrphan {
			t.Errorf("ProcessBlock incorrectly returned block %v "+
				"is an orphan\n", i)
			return
		}
	}

	// Insert an orphan block.
	_, isOrphan, err := chain.ProcessBlock(btcutil.NewBlock(&Block100000),
		BFNone)
	if err != nil {
		t.Errorf("Unable to process block: %v", err)
		return
	}
	if !isOrphan {
		t.Errorf("ProcessBlock indicated block is an not orphan when " +
			"it should be\n")
		return
	}

	tests := []struct {
		hash string
		want bool
	}{
		// Genesis block should be present (in the main chain).
		{hash: chaincfg.MainNetParams.GenesisHash.String(), want: true},

		// Block 3a should be present (on a side chain).
		{hash: "00000000474284d20067a4d33f6a02284e6ef70764a3a26d6a5b9df52ef663dd", want: true},

		// Block 100000 should be present (as an orphan).
		{hash: "000000000003ba27aa200b1cecaad478d2b00432346c3f1f3986da1afd33e506", want: true},

		// Random hashes should not be available.
		{hash: "123", want: false},
	}

	for i, test := range tests {
		hash, err := chainhash.NewHashFromStr(test.hash)
		if err != nil {
			t.Errorf("NewHashFromStr: %v", err)
			continue
		}

		result, err := chain.HaveBlock(hash)
		if err != nil {
			t.Errorf("HaveBlock #%d unexpected error: %v", i, err)
			return
		}
		if result != test.want {
			t.Errorf("HaveBlock #%d got %v want %v", i, result,
				test.want)
			continue
		}
	}
}

// TestCalcSequenceLock tests the LockTimeToSequence function, and the
// CalcSequenceLock method of a Chain instance. The tests exercise several
// combinations of inputs to the CalcSequenceLock function in order to ensure
// the returned SequenceLocks are correct for each test instance.
func TestCalcSequenceLock(t *testing.T) {
	netParams := &chaincfg.SimNetParams

	// We need to activate CSV in order to test the processing logic, so
	// manually craft the block version that's used to signal the soft-fork
	// activation.
	csvBit := netParams.Deployments[chaincfg.DeploymentCSV].BitNumber
	blockVersion := int32(0x20000000 | (uint32(1) << csvBit))

	// Generate enough synthetic blocks to activate CSV.
	chain := newFakeChain(netParams)
	node := chain.bestNode
	blockTime := node.Header().Timestamp
	numBlocksToActivate := (netParams.MinerConfirmationWindow * 3)
	for i := uint32(0); i < numBlocksToActivate; i++ {
		blockTime = blockTime.Add(time.Second)
		node = newFakeNode(node, blockVersion, 0, blockTime)
		chain.index.AddNode(node)
		chain.bestNode = node
	}

	// Create a utxo view with a fake utxo for the inputs used in the
	// transactions created below.  This utxo is added such that it has an
	// age of 4 blocks.
	targetTx := btcutil.NewTx(&wire.MsgTx{
		TxOut: []*wire.TxOut{{
			PkScript: nil,
			Value:    10,
		}},
	})
	utxoView := NewUtxoViewpoint()
	utxoView.AddTxOuts(targetTx, int32(numBlocksToActivate)-4)
	utxoView.SetBestHash(&node.hash)

	// Create a utxo that spends the fake utxo created above for use in the
	// transactions created in the tests.  It has an age of 4 blocks.  Note
	// that the sequence lock heights are always calculated from the same
	// point of view that they were originally calculated from for a given
	// utxo.  That is to say, the height prior to it.
	utxo := wire.OutPoint{
		Hash:  *targetTx.Hash(),
		Index: 0,
	}
	prevUtxoHeight := int32(numBlocksToActivate) - 4

	// Obtain the median time past from the PoV of the input created above.
	// The MTP for the input is the MTP from the PoV of the block *prior*
	// to the one that included it.
	medianTime := node.RelativeAncestor(5).CalcPastMedianTime().Unix()

	// The median time calculated from the PoV of the best block in the
	// test chain.  For unconfirmed inputs, this value will be used since
	// the MTP will be calculated from the PoV of the yet-to-be-mined
	// block.
	nextMedianTime := node.CalcPastMedianTime().Unix()
	nextBlockHeight := int32(numBlocksToActivate) + 1

	// Add an additional transaction which will serve as our unconfirmed
	// output.
	unConfTx := &wire.MsgTx{
		TxOut: []*wire.TxOut{{
			PkScript: nil,
			Value:    5,
		}},
	}
	unConfUtxo := wire.OutPoint{
		Hash:  unConfTx.TxHash(),
		Index: 0,
	}

	// Adding a utxo with a height of 0x7fffffff indicates that the output
	// is currently unmined.
	utxoView.AddTxOuts(btcutil.NewTx(unConfTx), 0x7fffffff)

	tests := []struct {
		tx      *wire.MsgTx
		view    *UtxoViewpoint
		mempool bool
		want    *SequenceLock
	}{
		// A transaction of version one should disable sequence locks
		// as the new sequence number semantics only apply to
		// transactions version 2 or higher.
		{
			tx: &wire.MsgTx{
				Version: 1,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(false, 3),
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     -1,
				BlockHeight: -1,
			},
		},
		// A transaction with a single input with max sequence number.
		// This sequence number has the high bit set, so sequence locks
		// should be disabled.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         wire.MaxTxInSequenceNum,
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     -1,
				BlockHeight: -1,
			},
		},
		// A transaction with a single input whose lock time is
		// expressed in seconds.  However, the specified lock time is
		// below the required floor for time based lock times since
		// they have time granularity of 512 seconds.  As a result, the
		// seconds lock-time should be just before the median time of
		// the targeted block.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(true, 2),
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     medianTime - 1,
				BlockHeight: -1,
			},
		},
		// A transaction with a single input whose lock time is
		// expressed in seconds.  The number of seconds should be 1023
		// seconds after the median past time of the last block in the
		// chain.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(true, 1024),
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     medianTime + 1023,
				BlockHeight: -1,
			},
		},
		// A transaction with multiple inputs.  The first input has a
		// lock time expressed in seconds.  The second input has a
		// sequence lock in blocks with a value of 4.  The last input
		// has a sequence number with a value of 5, but has the disable
		// bit set.  So the first lock should be selected as it's the
		// latest lock that isn't disabled.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(true, 2560),
				}, {
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(false, 4),
				}, {
					PreviousOutPoint: utxo,
					Sequence: LockTimeToSequence(false, 5) |
						wire.SequenceLockTimeDisabled,
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     medianTime + (5 << wire.SequenceLockTimeGranularity) - 1,
				BlockHeight: prevUtxoHeight + 3,
			},
		},
		// Transaction with a single input.  The input's sequence number
		// encodes a relative lock-time in blocks (3 blocks).  The
		// sequence lock should  have a value of -1 for seconds, but a
		// height of 2 meaning it can be included at height 3.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(false, 3),
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     -1,
				BlockHeight: prevUtxoHeight + 2,
			},
		},
		// A transaction with two inputs with lock times expressed in
		// seconds.  The selected sequence lock value for seconds should
		// be the time further in the future.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(true, 5120),
				}, {
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(true, 2560),
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     medianTime + (10 << wire.SequenceLockTimeGranularity) - 1,
				BlockHeight: -1,
			},
		},
		// A transaction with two inputs with lock times expressed in
		// blocks.  The selected sequence lock value for blocks should
		// be the height further in the future, so a height of 10
		// indicating it can be included at height 11.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(false, 1),
				}, {
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(false, 11),
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     -1,
				BlockHeight: prevUtxoHeight + 10,
			},
		},
		// A transaction with multiple inputs.  Two inputs are time
		// based, and the other two are block based. The lock lying
		// further into the future for both inputs should be chosen.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(true, 2560),
				}, {
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(true, 6656),
				}, {
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(false, 3),
				}, {
					PreviousOutPoint: utxo,
					Sequence:         LockTimeToSequence(false, 9),
				}},
			},
			view: utxoView,
			want: &SequenceLock{
				Seconds:     medianTime + (13 << wire.SequenceLockTimeGranularity) - 1,
				BlockHeight: prevUtxoHeight + 8,
			},
		},
		// A transaction with a single unconfirmed input.  As the input
		// is confirmed, the height of the input should be interpreted
		// as the height of the *next* block.  So, a 2 block relative
		// lock means the sequence lock should be for 1 block after the
		// *next* block height, indicating it can be included 2 blocks
		// after that.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: unConfUtxo,
					Sequence:         LockTimeToSequence(false, 2),
				}},
			},
			view:    utxoView,
			mempool: true,
			want: &SequenceLock{
				Seconds:     -1,
				BlockHeight: nextBlockHeight + 1,
			},
		},
		// A transaction with a single unconfirmed input.  The input has
		// a time based lock, so the lock time should be based off the
		// MTP of the *next* block.
		{
			tx: &wire.MsgTx{
				Version: 2,
				TxIn: []*wire.TxIn{{
					PreviousOutPoint: unConfUtxo,
					Sequence:         LockTimeToSequence(true, 1024),
				}},
			},
			view:    utxoView,
			mempool: true,
			want: &SequenceLock{
				Seconds:     nextMedianTime + 1023,
				BlockHeight: -1,
			},
		},
	}

	t.Logf("Running %v SequenceLock tests", len(tests))
	for i, test := range tests {
		utilTx := btcutil.NewTx(test.tx)
		seqLock, err := chain.CalcSequenceLock(utilTx, test.view, test.mempool)
		if err != nil {
			t.Fatalf("test #%d, unable to calc sequence lock: %v", i, err)
		}

		if seqLock.Seconds != test.want.Seconds {
			t.Fatalf("test #%d got %v seconds want %v seconds",
				i, seqLock.Seconds, test.want.Seconds)
		}
		if seqLock.BlockHeight != test.want.BlockHeight {
			t.Fatalf("test #%d got height of %v want height of %v ",
				i, seqLock.BlockHeight, test.want.BlockHeight)
		}
	}
}
