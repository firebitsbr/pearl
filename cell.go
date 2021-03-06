package pearl

import (
	"bufio"
	"encoding/binary"
	"io"

	"github.com/mmcloughlin/pearl/log"
	"github.com/pkg/errors"
)

// MaxPayloadLength is the longest allowable cell payload.
//
// Reference: https://github.com/torproject/torspec/blob/master/tor-spec.txt#L65
//
//	   PAYLOAD_LEN -- The longest allowable cell payload, in bytes. (509)
//
const MaxPayloadLength = 509

var ErrUnknownCommand = errors.New("unknown command")

type CircID uint32

type CellFormat interface {
	CircIDLen() int
	CircID([]byte) CircID
	PutCircID([]byte, CircID)
}

type CircID2Format struct{}

func (c CircID2Format) CircIDLen() int {
	return 2
}

func (c CircID2Format) CircID(x []byte) CircID {
	return CircID(binary.BigEndian.Uint16(x))
}

func (c CircID2Format) PutCircID(x []byte, id CircID) {
	// BUG(mmcloughlin): potential overflow in CircID2Format.PutCircID with a
	// 32-bit circ id value.
	binary.BigEndian.PutUint16(x, uint16(id))
}

type CircID4Format struct{}

func (c CircID4Format) CircIDLen() int {
	return 4
}

func (c CircID4Format) CircID(x []byte) CircID {
	return CircID(binary.BigEndian.Uint32(x))
}

func (c CircID4Format) PutCircID(x []byte, id CircID) {
	binary.BigEndian.PutUint32(x, uint32(id))
}

var VersionsCellFormat CellFormat = CircID2Format{}

// IsCommandVariableLength determines whether a cell for the given command
// code is variable length.
func IsCommandVariableLength(c Command) bool {
	// Reference: https://github.com/torproject/torspec/blob/master/tor-spec.txt#L406-L409
	//
	//	   On a version 2 connection, variable-length cells are indicated by a
	//	   command byte equal to 7 ("VERSIONS").  On a version 3 or
	//	   higher connection, variable-length cells are indicated by a command
	//	   byte equal to 7 ("VERSIONS"), or greater than or equal to 128.
	//
	return (c == Versions) || (byte(c) >= 128)
}

func PayloadOffset(f CellFormat, cmd Command) int {
	offset := f.CircIDLen() + 1
	if IsCommandVariableLength(cmd) {
		offset += 2
	}
	return offset
}

type Cell interface {
	CircID() CircID
	Command() Command
	Payload() []byte
	Bytes() []byte
}

type CellBuilder interface {
	Cell(CellFormat) (Cell, error)
}

type cell struct {
	format CellFormat
	data   []byte
}

var _ Cell = new(cell)

func NewCellFromBuffer(f CellFormat, x []byte) Cell {
	return cell{
		format: f,
		data:   x,
	}
}

// XXX rename to NewVarCell ?
func NewCellEmptyPayload(f CellFormat, circID CircID, cmd Command, n uint16) Cell {
	// BUG(mmcloughlin): NewCellEmptyPayload should use sync.Pool to allocate
	// cell buffers.
	alloc := f.CircIDLen() + 1 + 2 + int(n)
	data := make([]byte, alloc) // assumes we need 2 bytes for length (but we may not)
	ptr := 0

	f.PutCircID(data[ptr:], circID)
	ptr += f.CircIDLen()

	data[f.CircIDLen()] = byte(cmd)
	ptr++

	if IsCommandVariableLength(cmd) {
		binary.BigEndian.PutUint16(data[ptr:], n)
		ptr += 2
	}

	return NewCellFromBuffer(f, data)
}

func NewFixedCell(f CellFormat, circID CircID, cmd Command) Cell {
	if IsCommandVariableLength(cmd) {
		panic("command is requires variable length cell")
	}

	// BUG(mmcloughlin): NewFixedCell should use sync.Pool to allocate
	// cell buffers.
	alloc := f.CircIDLen() + 1 + MaxPayloadLength
	data := make([]byte, alloc) // assumes we need 2 bytes for length (but we may not)
	ptr := 0

	f.PutCircID(data[ptr:], circID)
	ptr += f.CircIDLen()

	data[f.CircIDLen()] = byte(cmd)
	ptr++

	return NewCellFromBuffer(f, data)
}

func (c cell) CircID() CircID {
	return c.format.CircID(c.data)
}

func (c cell) Command() Command {
	return Command(c.data[c.format.CircIDLen()])
}

func (c cell) Payload() []byte {
	offset := PayloadOffset(c.format, c.Command())
	return c.data[offset:]
}

func (c cell) Bytes() []byte {
	return c.data
}

type CellReader interface {
	ReadCell(CellFormat) (Cell, error)
}

type CellReaderFunc func(CellFormat) (Cell, error)

func (r CellReaderFunc) ReadCell(f CellFormat) (Cell, error) {
	return r(f)
}

type cellReader struct {
	reader io.Reader

	buf    *bufio.Reader
	logger log.Logger
}

var _ CellReader = new(cellReader)

func NewCellReader(r io.Reader, logger log.Logger) CellReader {
	return cellReader{
		reader: r,

		buf:    bufio.NewReader(r),
		logger: log.ForComponent(logger, "cellreader"),
	}
}

func (r cellReader) ReadCell(format CellFormat) (Cell, error) {
	// Reference: https://github.com/torproject/torspec/blob/master/tor-spec.txt#L391-L404
	//
	//	   On a version 1 connection, each cell contains the following
	//	   fields:
	//
	//	        CircID                                [CIRCID_LEN bytes]
	//	        Command                               [1 byte]
	//	        Payload (padded with 0 bytes)         [PAYLOAD_LEN bytes]
	//
	//	   On a version 2 or higher connection, all cells are as in version 1
	//	   connections, except for variable-length cells, whose format is:
	//
	//	        CircID                                [CIRCID_LEN octets]
	//	        Command                               [1 octet]
	//	        Length                                [2 octets; big-endian integer]
	//	        Payload                               [Length bytes]
	//

	circIDLen := format.CircIDLen()

	// Read cell header
	n := circIDLen + 1 + 2
	hdr, err := r.buf.Peek(n)
	if err != nil {
		return nil, errors.Wrap(err, "could not peek cell header")
	}
	r.logger.With("hdr", hdr).Trace("peek cell header")

	// command byte
	cmdByte := hdr[circIDLen]
	if !IsCommand(cmdByte) {
		return nil, ErrUnknownCommand
	}
	cmd := Command(cmdByte)
	r.logger.With("command", cmd.String()).Trace("extracted command")

	// fixed vs. variable cell
	payloadLen := uint16(MaxPayloadLength)
	if IsCommandVariableLength(cmd) {
		payloadLen = binary.BigEndian.Uint16(hdr[circIDLen+1:])
	}
	payloadOffset := PayloadOffset(format, cmd)

	// actually read the cell
	cellLength := payloadOffset + int(payloadLen)
	r.logger.With("len", cellLength).Trace("reading cell")

	// BUG(mmcloughlin) cellReader.ReadCell allocates new buffer every time
	// (should use sync.Pool)
	cellBuf := make([]byte, cellLength)
	_, err = io.ReadFull(r.buf, cellBuf)
	if err != nil {
		return nil, errors.Wrap(err, "could not read full cell")
	}

	return NewCellFromBuffer(format, cellBuf), nil
}
