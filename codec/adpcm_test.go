package codec

import (
	"math"
	"testing"
)

// sineSamples generates n samples of a 16-bit sine wave at freqHz, sampled
// at 16 kHz, at the given amplitude fraction of full scale.
func sineSamples(n int, freqHz, amplitude float64) []int16 {
	const sampleRate = 16000.0
	out := make([]int16, n)
	for i := range out {
		t := float64(i) / sampleRate
		v := amplitude * 32767.0 * math.Sin(2*math.Pi*freqHz*t)
		out[i] = int16(v)
	}
	return out
}

func snrDB(t *testing.T, ref, got []int16) float64 {
	t.Helper()
	if len(ref) != len(got) {
		t.Fatalf("length mismatch: ref=%d got=%d", len(ref), len(got))
	}
	var signal, noise float64
	for i := range ref {
		s := float64(ref[i])
		e := float64(ref[i]) - float64(got[i])
		signal += s * s
		noise += e * e
	}
	if noise == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(signal/noise)
}

// TestRoundTripSNR encodes and decodes a reference tone spanning several
// frames and checks the reconstruction stays within IMA ADPCM's expected
// fidelity envelope (T1: "encode->decode round-trip stays within expected
// SNR for a reference sample").
func TestRoundTripSNR(t *testing.T) {
	ref := sineSamples(SamplesPerFrame*4, 440, 0.5)

	var state State
	packed, _ := Encode(ref, state)
	decoded, _ := Decode(packed, len(ref), state)

	snr := snrDB(t, ref, decoded)
	const minSNR = 20.0 // IMA ADPCM on a clean tone typically lands ~25-30 dB
	if snr < minSNR {
		t.Errorf("round-trip SNR = %.2f dB, want >= %.2f dB", snr, minSNR)
	}
	t.Logf("round-trip SNR: %.2f dB", snr)
}

// TestEncodeDeterministic proves the encoder is bit-exact and
// deterministic: identical input yields identical output across runs.
func TestEncodeDeterministic(t *testing.T) {
	samples := sineSamples(SamplesPerFrame*3, 660, 0.7)

	packed1, state1 := Encode(samples, State{})
	packed2, state2 := Encode(samples, State{})

	if len(packed1) != len(packed2) {
		t.Fatalf("length differs: %d vs %d", len(packed1), len(packed2))
	}
	for i := range packed1 {
		if packed1[i] != packed2[i] {
			t.Fatalf("byte %d differs: %#x vs %#x", i, packed1[i], packed2[i])
		}
	}
	if state1 != state2 {
		t.Fatalf("final state differs: %+v vs %+v", state1, state2)
	}
}

// TestDecodeDeterministic proves the decoder is likewise bit-exact and
// deterministic.
func TestDecodeDeterministic(t *testing.T) {
	samples := sineSamples(SamplesPerFrame*3, 660, 0.7)
	packed, _ := Encode(samples, State{})

	decoded1, state1 := Decode(packed, len(samples), State{})
	decoded2, state2 := Decode(packed, len(samples), State{})

	for i := range decoded1 {
		if decoded1[i] != decoded2[i] {
			t.Fatalf("sample %d differs: %d vs %d", i, decoded1[i], decoded2[i])
		}
	}
	if state1 != state2 {
		t.Fatalf("final state differs: %+v vs %+v", state1, state2)
	}
}

// TestReseedMatchesContinuousDecode is the loss-independence proof required
// by testing.md: a decoder that re-seeds mid-stream from a frame's
// transmitted predictor/step-index must reproduce exactly what a decoder
// that processed every prior frame would have produced, from that point
// forward. This is what makes a single lost packet non-fatal.
func TestReseedMatchesContinuousDecode(t *testing.T) {
	const numFrames = 8
	ref := sineSamples(SamplesPerFrame*numFrames, 523, 0.6)

	// Encode frame by frame, recording the state transmitted with each
	// frame (i.e. the state *before* that frame was encoded) exactly as
	// spec.md §4.1's adpcm_predictor/adpcm_step_index fields would carry
	// it on the wire.
	type frame struct {
		packed      []byte
		stateBefore State
	}
	frames := make([]frame, numFrames)
	state := State{}
	for i := range numFrames {
		start := i * SamplesPerFrame
		end := start + SamplesPerFrame
		before := state
		var packed []byte
		packed, state = Encode(ref[start:end], before)
		frames[i] = frame{packed: packed, stateBefore: before}
	}

	// Continuous decode: process every frame in order from a fresh
	// decoder, exactly as a receiver that dropped nothing would.
	continuous := make([]int16, 0, len(ref))
	dstate := State{}
	for _, f := range frames {
		var out []int16
		out, dstate = Decode(f.packed, SamplesPerFrame, dstate)
		continuous = append(continuous, out...)
	}

	// Re-seeded decode: pretend every frame before frameIdx was lost, and
	// resume decoding using only the state carried in frameIdx itself.
	for frameIdx := 1; frameIdx < numFrames; frameIdx++ {
		reseeded := make([]int16, 0, (numFrames-frameIdx)*SamplesPerFrame)
		rstate := frames[frameIdx].stateBefore
		for i := frameIdx; i < numFrames; i++ {
			var out []int16
			out, rstate = Decode(frames[i].packed, SamplesPerFrame, rstate)
			reseeded = append(reseeded, out...)
		}

		want := continuous[frameIdx*SamplesPerFrame:]
		if len(reseeded) != len(want) {
			t.Fatalf("frameIdx=%d: length mismatch: reseeded=%d want=%d", frameIdx, len(reseeded), len(want))
		}
		for i := range want {
			if reseeded[i] != want[i] {
				t.Fatalf("frameIdx=%d: sample %d diverges after reseed: got %d, want %d",
					frameIdx, i, reseeded[i], want[i])
			}
		}
	}
}

// TestSilenceNoPanic checks an all-zero frame encodes and decodes cleanly
// with no drift away from zero.
func TestSilenceNoPanic(t *testing.T) {
	samples := make([]int16, SamplesPerFrame)
	packed, _ := Encode(samples, State{})
	decoded, _ := Decode(packed, len(samples), State{})
	for i, v := range decoded {
		if v != 0 {
			t.Fatalf("sample %d: got %d, want 0 for silence", i, v)
		}
	}
}

// TestFullScaleNoOverflow drives the encoder with a constant full-scale
// signal (and its negative) to check the predictor clamps instead of
// wrapping. A decoded value is already typed int16, so it can never
// itself be "out of int16 range" — the real risk is the intermediate
// int32 predictor overflowing before clampInt16 catches it, which would
// surface as the step index escaping its table bounds or the decoded
// signal wrapping to the opposite sign of a sustained one-sided input.
func TestFullScaleNoOverflow(t *testing.T) {
	for _, amp := range []int16{32767, -32768} {
		samples := make([]int16, SamplesPerFrame)
		for i := range samples {
			samples[i] = amp
		}
		packed, encState := Encode(samples, State{})
		decoded, decState := Decode(packed, len(samples), State{})

		if encState.StepIndex > 88 {
			t.Fatalf("amp=%d: encoder step index %d exceeds table bound", amp, encState.StepIndex)
		}
		if decState.StepIndex > 88 {
			t.Fatalf("amp=%d: decoder step index %d exceeds table bound", amp, decState.StepIndex)
		}
		for i, v := range decoded {
			if (amp > 0 && v < 0) || (amp < 0 && v > 0) {
				t.Fatalf("amp=%d: sample %d decoded to %d — wrong sign, predictor wrapped instead of clamping", amp, i, v)
			}
		}
	}
}

// TestClippingSquareWaveNoPanic drives worst-case sample-to-sample deltas
// (alternating full-scale-positive/full-scale-negative) — the largest
// possible diff the quantizer can see — and checks for no panic and that
// the codec's internal state (predictor/step index) stays within valid
// bounds rather than overflowing.
func TestClippingSquareWaveNoPanic(t *testing.T) {
	samples := make([]int16, SamplesPerFrame)
	for i := range samples {
		if i%2 == 0 {
			samples[i] = 32767
		} else {
			samples[i] = -32768
		}
	}
	packed, encState := Encode(samples, State{})
	_, decState := Decode(packed, len(samples), State{})

	if encState.StepIndex > 88 {
		t.Fatalf("encoder step index %d exceeds table bound", encState.StepIndex)
	}
	if decState.StepIndex > 88 {
		t.Fatalf("decoder step index %d exceeds table bound", decState.StepIndex)
	}
}

// TestFrameSizeMatchesSpec locks in the wire-format constant from
// spec.md §4.1: 400 samples pack into exactly 200 bytes.
func TestFrameSizeMatchesSpec(t *testing.T) {
	if SamplesPerFrame != 400 {
		t.Errorf("SamplesPerFrame = %d, want 400", SamplesPerFrame)
	}
	if FrameBytes != 200 {
		t.Errorf("FrameBytes = %d, want 200", FrameBytes)
	}
	samples := make([]int16, SamplesPerFrame)
	packed, _ := Encode(samples, State{})
	if len(packed) != FrameBytes {
		t.Errorf("Encode produced %d bytes, want %d", len(packed), FrameBytes)
	}
}
