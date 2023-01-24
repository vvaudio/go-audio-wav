package wav

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/riff"
	//"github.com/go-audio/wav"
)

// Encoder encodes LPCM data into a wav containter.
type FixedLengthEncoder struct {
	e        Encoder
	w2       io.Writer
	dataSize int // predetermined size of data chunk in bytes
}

// NewEncoder creates a new encoder to create a new wav file.
// Don't forget to add Frames to the encoder before writing.
func NewFixedLengthEncoder(w io.Writer, sampleRate, bitDepth, numChans, audioFormat int, dataSize int) *FixedLengthEncoder {
	return &FixedLengthEncoder{
		Encoder{
			buf:            bytes.NewBuffer(make([]byte, 0, bytesNumFromDuration(time.Minute, sampleRate, bitDepth)*numChans)),
			SampleRate:     sampleRate,
			BitDepth:       bitDepth,
			NumChans:       numChans,
			WavAudioFormat: audioFormat,
		},
		w,
		dataSize,
	}
}

// AddLE serializes and adds the passed value using little endian
func (e *FixedLengthEncoder) AddLE(src interface{}) error {
	e.e.WrittenBytes += binary.Size(src)
	return binary.Write(e.w2, binary.LittleEndian, src)
}

// AddBE serializes and adds the passed value using big endian
func (e *FixedLengthEncoder) AddBE(src interface{}) error {
	e.e.WrittenBytes += binary.Size(src)
	return binary.Write(e.w2, binary.BigEndian, src)
}

func (e *FixedLengthEncoder) addBuffer(buf *audio.IntBuffer) error {
	if buf == nil {
		return fmt.Errorf("can't add a nil buffer")
	}

	frameCount := buf.NumFrames()
	// performance tweak: setup a buffer so we don't do too many writes
	var err error
	for i := 0; i < frameCount; i++ {
		for j := 0; j < buf.Format.NumChannels; j++ {
			v := buf.Data[i*buf.Format.NumChannels+j]
			switch e.e.BitDepth {
			case 8:
				if err = binary.Write(e.e.buf, binary.LittleEndian, uint8(v)); err != nil {
					return err
				}
			case 16:
				if err = binary.Write(e.e.buf, binary.LittleEndian, int16(v)); err != nil {
					return err
				}
			case 24:
				if err = binary.Write(e.e.buf, binary.LittleEndian, audio.Int32toInt24LEBytes(int32(v))); err != nil {
					return err
				}
			case 32:
				if err = binary.Write(e.e.buf, binary.LittleEndian, int32(v)); err != nil {
					return err
				}
			default:
				return fmt.Errorf("can't add frames of bit size %d", e.e.BitDepth)
			}
		}
		e.e.frames++
	}
	if n, err := e.w2.Write(e.e.buf.Bytes()); err != nil {
		e.e.WrittenBytes += n
		return err
	}
	e.e.WrittenBytes += e.e.buf.Len()
	e.e.buf.Reset()

	return nil
}

func (e *FixedLengthEncoder) writeHeader() error {
	if e.e.wroteHeader {
		return errors.New("already wrote header")
	}
	e.e.wroteHeader = true
	if e == nil {
		return fmt.Errorf("can't write a nil encoder")
	}
	if e.w2 == nil {
		return fmt.Errorf("can't write to a nil writer")
	}

	if e.e.WrittenBytes > 0 {
		return nil
	}

	// riff ID
	if err := e.AddLE(riff.RiffID); err != nil {
		return err
	}
	// size of first chunk with header minus chunk header and size (size + 44 - 8)
	chunksize := e.dataSize + 36
	if err := e.AddLE(uint32(chunksize)); err != nil {
		return err
	}
	// wave headers
	if err := e.AddLE(riff.WavFormatID); err != nil {
		return err
	}
	// form
	if err := e.AddLE(riff.FmtID); err != nil {
		return err
	}
	// chunk size
	if err := e.AddLE(uint32(16)); err != nil {
		return err
	}
	// wave format
	if err := e.AddLE(uint16(e.e.WavAudioFormat)); err != nil {
		return err
	}
	// num channels
	if err := e.AddLE(uint16(e.e.NumChans)); err != nil {
		return fmt.Errorf("error encoding the number of channels - %v", err)
	}
	// samplerate
	if err := e.AddLE(uint32(e.e.SampleRate)); err != nil {
		return fmt.Errorf("error encoding the sample rate - %v", err)
	}
	blockAlign := e.e.NumChans * e.e.BitDepth / 8
	// avg bytes per sec
	if err := e.AddLE(uint32(e.e.SampleRate * blockAlign)); err != nil {
		return fmt.Errorf("error encoding the avg bytes per sec - %v", err)
	}
	// block align
	if err := e.AddLE(uint16(blockAlign)); err != nil {
		return err
	}
	// bits per sample
	if err := e.AddLE(uint16(e.e.BitDepth)); err != nil {
		return fmt.Errorf("error encoding bits per sample - %v", err)
	}

	return nil
}

// Write encodes and writes the passed buffer to the underlying writer.
// Don't forget to Close() the encoder or the file won't be valid.
func (e *FixedLengthEncoder) Write(buf *audio.IntBuffer) error {
	if !e.e.wroteHeader {
		if err := e.writeHeader(); err != nil {
			return err
		}
	}

	if !e.e.pcmChunkStarted {
		// sound header
		if err := e.AddLE(riff.DataFormatID); err != nil {
			return fmt.Errorf("error encoding sound header %v", err)
		}
		e.e.pcmChunkStarted = true

		// write chunksize
		if err := e.AddLE(uint32(e.dataSize)); err != nil {
			return fmt.Errorf("%v when writing wav data chunk size header", err)
		}
	}

	return e.addBuffer(buf)
}

// WriteFrame writes a single frame of data to the underlying writer.
func (e *FixedLengthEncoder) WriteFrame(value interface{}) error {
	if !e.e.wroteHeader {
		e.writeHeader()
	}
	if !e.e.pcmChunkStarted {
		// sound header
		if err := e.AddLE(riff.DataFormatID); err != nil {
			return fmt.Errorf("error encoding sound header %v", err)
		}
		e.e.pcmChunkStarted = true

		// write chunksize
		if err := e.AddLE(uint32(e.dataSize)); err != nil {
			return fmt.Errorf("%v when writing wav data chunk size header", err)
		}
	}

	e.e.frames++
	return e.AddLE(value)
}

// WriteBytes writes from a reader, assumed to be in the right format, to the underlying writer.
func (e *FixedLengthEncoder) WriteBytes(r io.Reader) error {
	if !e.e.wroteHeader {
		e.writeHeader()
	}
	if !e.e.pcmChunkStarted {
		// sound header
		if err := e.AddLE(riff.DataFormatID); err != nil {
			return fmt.Errorf("error encoding sound header %v", err)
		}
		e.e.pcmChunkStarted = true

		// write chunksize
		if err := e.AddLE(uint32(e.dataSize)); err != nil {
			return fmt.Errorf("%v when writing wav data chunk size header", err)
		}
	}

	//e.e.frames++
	_, err := io.Copy(e.w2, r)
	return err
}

func (e *FixedLengthEncoder) writeMetadata() error {
	chunkData := encodeInfoChunk(&e.e)
	if err := e.AddBE(CIDList); err != nil {
		return fmt.Errorf("failed to write the LIST chunk ID: %s", err)
	}
	if err := e.AddLE(uint32(len(chunkData))); err != nil {
		return fmt.Errorf("failed to write the LIST chunk size: %s", err)
	}
	return e.AddBE(chunkData)
}

// Close flushes the content to disk, make sure the headers are up to date
// Note that the underlying writer is NOT being closed.
func (e *FixedLengthEncoder) Close() error {
	if e == nil || e.w2 == nil {
		return nil
	}

	// inject metadata at the end to not trip implementation not supporting
	// metadata chunks
	if e.e.Metadata != nil {
		if err := e.writeMetadata(); err != nil {
			return fmt.Errorf("failed to write metadata - %v", err)
		}
	}

	switch e.w2.(type) {
	case *os.File:
		return e.w2.(*os.File).Sync()
	}
	return nil
}
