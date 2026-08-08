// Package codec implements IMA ADPCM encoding and decoding at 16 kHz mono,
// 4 bits/sample, per ADR-0004. It is pure integer arithmetic (the target
// has no FPU) with no I/O and no imports outside the standard library, so
// it runs identically under `go test` on a laptop and under TinyGo on the
// ESP32-C5.
//
// The codec is a stateful predictor: State must be carried alongside every
// encoded frame (see spec.md §4.1) so a receiver that missed prior frames
// can re-seed and decode independently, rather than accumulating
// unbounded drift from a single lost packet.
package codec

// SamplesPerFrame is the number of 16 kHz PCM samples in one 25 ms frame.
const SamplesPerFrame = 400

// FrameBytes is the packed size of one encoded frame: two 4-bit samples
// per byte. Must equal the 200-byte ADPCM payload in spec.md §4.1.
const FrameBytes = SamplesPerFrame / 2

// State is the IMA ADPCM codec state: the predictor and quantizer step
// index. It is re-seeded from every wire frame (spec.md §4.1 fields
// adpcm_predictor / adpcm_step_index), so its field types match the wire
// encoding exactly.
type State struct {
	Predictor int16
	StepIndex uint8
}

var indexTable = [16]int8{
	-1, -1, -1, -1, 2, 4, 6, 8,
	-1, -1, -1, -1, 2, 4, 6, 8,
}

var stepSizeTable = [89]int32{
	7, 8, 9, 10, 11, 12, 13, 14, 16, 17,
	19, 21, 23, 25, 28, 31, 34, 37, 41, 45,
	50, 55, 60, 66, 73, 80, 88, 97, 107, 118,
	130, 143, 157, 173, 190, 209, 230, 253, 279, 307,
	337, 371, 408, 449, 494, 544, 598, 658, 724, 796,
	876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066,
	2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871, 5358,
	5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899,
	15289, 16818, 18500, 20350, 22385, 24623, 27086, 29794, 32767,
}

func clampInt16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func clampIndex(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 88 {
		return 88
	}
	return uint8(v)
}

// EncodeSample quantizes one PCM sample against the given state, returning
// the 4-bit code (nibble) and the updated state. The encoder reconstructs
// its own predictor from the quantized code — the same value a decoder
// would compute — so encoder and decoder state stay bit-exact in lockstep.
func EncodeSample(sample int16, state State) (code uint8, next State) {
	valpred := int32(state.Predictor)
	index := int32(state.StepIndex)
	step := stepSizeTable[index]

	diff := int32(sample) - valpred
	sign := int32(0)
	if diff < 0 {
		sign = 8
		diff = -diff
	}

	delta := int32(0)
	vpdiff := step >> 3

	if diff >= step {
		delta = 4
		diff -= step
		vpdiff += step
	}
	step >>= 1
	if diff >= step {
		delta |= 2
		diff -= step
		vpdiff += step
	}
	step >>= 1
	if diff >= step {
		delta |= 1
		vpdiff += step
	}

	if sign != 0 {
		valpred -= vpdiff
	} else {
		valpred += vpdiff
	}
	valpred32 := clampInt16(valpred)

	delta |= sign
	index += int32(indexTable[delta])

	next = State{
		Predictor: valpred32,
		StepIndex: clampIndex(index),
	}
	return uint8(delta), next
}

// DecodeSample reconstructs one PCM sample from a 4-bit code and the given
// state, returning the sample and the updated state.
func DecodeSample(code uint8, state State) (sample int16, next State) {
	code &= 0x0f
	index := int32(state.StepIndex)
	step := stepSizeTable[index]

	index += int32(indexTable[code])

	sign := code & 8
	mag := int32(code & 7)

	vpdiff := step >> 3
	if mag&4 != 0 {
		vpdiff += step
	}
	if mag&2 != 0 {
		vpdiff += step >> 1
	}
	if mag&1 != 0 {
		vpdiff += step >> 2
	}

	valpred := int32(state.Predictor)
	if sign != 0 {
		valpred -= vpdiff
	} else {
		valpred += vpdiff
	}
	valpred16 := clampInt16(valpred)

	next = State{
		Predictor: valpred16,
		StepIndex: clampIndex(index),
	}
	return valpred16, next
}

// Encode packs samples into 4-bit ADPCM codes, two samples per byte
// (first sample in the high nibble), starting from state. It returns the
// packed bytes and the state after the last sample, which the caller
// carries forward as the *next* frame's re-seed fields.
//
// len(samples) need not be even; a trailing odd sample is packed into the
// low nibble of a final byte with the high nibble zeroed.
func Encode(samples []int16, state State) (packed []byte, next State) {
	packed = make([]byte, (len(samples)+1)/2)
	for i, s := range samples {
		var code uint8
		code, state = EncodeSample(s, state)
		if i%2 == 0 {
			packed[i/2] = code << 4
		} else {
			packed[i/2] |= code
		}
	}
	return packed, state
}

// Decode unpacks n samples from packed ADPCM bytes starting from state. It
// returns the decoded samples and the state after the last sample.
//
// n must not exceed 2*len(packed).
func Decode(packed []byte, n int, state State) (samples []int16, next State) {
	samples = make([]int16, n)
	for i := 0; i < n; i++ {
		b := packed[i/2]
		var code uint8
		if i%2 == 0 {
			code = b >> 4
		} else {
			code = b & 0x0f
		}
		samples[i], state = DecodeSample(code, state)
	}
	return samples, state
}
