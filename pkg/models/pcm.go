// Copyright 2025 VeloxVOIP.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package models

import (
	"encoding/binary"
	"io"

	msdk "github.com/livekit/media-sdk"
)

// BytesToPCM16Into converts bytes to PCM16Sample using a pre-allocated buffer.
// If sample is nil or has insufficient capacity, it allocates a new buffer.
// Returns the slice that was written to (may be reallocated).
//
// This follows livekit media-sdk buffer reuse patterns for zero-allocation audio processing:
//
//	var sample msdk.PCM16Sample
//	for {
//	    buf := readAudioBytes()
//	    sample = BytesToPCM16Into(buf, sample) // Reuses capacity when possible
//	    processAudio(sample)
//	}
//
// The conversion uses binary.LittleEndian to match PCM16Sample.CopyTo's encoding.
func BytesToPCM16Into(buf []byte, sample msdk.PCM16Sample) msdk.PCM16Sample {
	if len(buf)%2 != 0 {
		// Invalid input - return empty sample
		return nil
	}

	needed := len(buf) / 2

	// Reallocate if nil or insufficient capacity
	if cap(sample) < needed {
		sample = make(msdk.PCM16Sample, needed)
	} else {
		sample = sample[:needed] // Reslice to needed length
	}

	// Convert little-endian bytes to int16 samples
	// This matches the encoding used by PCM16Sample.CopyTo
	for i := 0; i < len(buf); i += 2 {
		sample[i/2] = int16(binary.LittleEndian.Uint16(buf[i : i+2]))
	}

	return sample
}

// PCM16ToBytesInto converts PCM16Sample to bytes using a pre-allocated buffer.
// If buf is nil or has insufficient capacity, it allocates a new buffer.
// Returns the slice that was written to (may be reallocated).
//
// This follows livekit media-sdk patterns for efficient buffer reuse:
//
//	var buf []byte
//	for {
//	    sample := readPCM16Sample()
//	    buf, err := PCM16ToBytesInto(sample, buf) // Reuses capacity when possible
//	    if err != nil {
//	        return err
//	    }
//	    writeBytes(buf)
//	}
//
// The conversion uses PCM16Sample.CopyTo which ensures proper little-endian encoding.
func PCM16ToBytesInto(sample msdk.PCM16Sample, buf []byte) ([]byte, error) {
	needed := sample.Size()

	// Reallocate if nil or insufficient capacity
	if cap(buf) < needed {
		buf = make([]byte, needed)
	} else {
		buf = buf[:needed] // Reslice to needed length
	}

	// Use the PCM16Sample's CopyTo method for proper conversion
	// This matches livekit media-sdk's standard approach
	n, err := sample.CopyTo(buf)
	if err != nil {
		return nil, err
	}

	if n != needed {
		return nil, io.ErrShortWrite
	}

	return buf, nil
}
