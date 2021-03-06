package pearl

import (
	"encoding/binary"

	"github.com/mmcloughlin/openssl"
	"github.com/pkg/errors"
)

// Reference: https://github.com/torproject/torspec/blob/master/tor-spec.txt#L544-L599
//
//	4.2. CERTS cells
//
//	   The CERTS cell describes the keys that a Tor instance is claiming
//	   to have.  It is a variable-length cell.  Its payload format is:
//
//	        N: Number of certs in cell            [1 octet]
//	        N times:
//	           CertType                           [1 octet]
//	           CLEN                               [2 octets]
//	           Certificate                        [CLEN octets]
//
//	   Any extra octets at the end of a CERTS cell MUST be ignored.
//
//	     CertType values are:
//	        1: Link key certificate certified by RSA1024 identity
//	        2: RSA1024 Identity certificate
//	        3: RSA1024 AUTHENTICATE cell link certificate
//
//	   The certificate format for the above certificate types is DER encoded
//	   X509.
//
//	   A CERTS cell may have no more than one certificate of each CertType.
//
//	   To authenticate the responder, the initiator MUST check the following:
//	     * The CERTS cell contains exactly one CertType 1 "Link" certificate.
//	     * The CERTS cell contains exactly one CertType 2 "ID" certificate.
//	     * Both certificates have validAfter and validUntil dates that
//	       are not expired.
//	     * The certified key in the Link certificate matches the
//	       link key that was used to negotiate the TLS connection.
//	     * The certified key in the ID certificate is a 1024-bit RSA key.
//	     * The certified key in the ID certificate was used to sign both
//	       certificates.
//	     * The link certificate is correctly signed with the key in the
//	       ID certificate
//	     * The ID certificate is correctly self-signed.
//	   Checking these conditions is sufficient to authenticate that the
//	   initiator is talking to the Tor node with the expected identity,
//	   as certified in the ID certificate.
//
//	   To authenticate the initiator, the responder MUST check the
//	   following:
//	     * The CERTS cell contains exactly one CertType 3 "AUTH" certificate.
//	     * The CERTS cell contains exactly one CertType 2 "ID" certificate.
//	     * Both certificates have validAfter and validUntil dates that
//	       are not expired.
//	     * The certified key in the AUTH certificate is a 1024-bit RSA key.
//	     * The certified key in the ID certificate is a 1024-bit RSA key.
//	     * The certified key in the ID certificate was used to sign both
//	       certificates.
//	     * The auth certificate is correctly signed with the key in the
//	       ID certificate.
//	     * The ID certificate is correctly self-signed.
//	   Checking these conditions is NOT sufficient to authenticate that the
//	   initiator has the ID it claims; to do so, the cells in 4.3 and 4.4
//	   below must be exchanged.
//

type CertType uint8

// Reference: https://github.com/torproject/torspec/blob/master/tor-spec.txt#L557-L560
//
//	     CertType values are:
//	        1: Link key certificate certified by RSA1024 identity
//	        2: RSA1024 Identity certificate
//	        3: RSA1024 AUTHENTICATE cell link certificate
//
var (
	LinkCert     CertType = 1
	IdentityCert CertType = 2
	AuthCert     CertType = 3
)

type CertCellEntry struct {
	Type CertType
	Cert *openssl.Certificate
}

type CertsCell struct {
	Certs []CertCellEntry
}

var _ CellBuilder = new(CertsCell)

func (c *CertsCell) AddCert(t CertType, crt *openssl.Certificate) {
	c.Certs = append(c.Certs, CertCellEntry{
		Type: t,
		Cert: crt,
	})
}

func (c CertsCell) Cell(f CellFormat) (Cell, error) {
	// Reference: https://github.com/torproject/torspec/blob/master/tor-spec.txt#L549-L553
	//
	//	        N: Number of certs in cell            [1 octet]
	//	        N times:
	//	           CertType                           [1 octet]
	//	           CLEN                               [2 octets]
	//	           Certificate                        [CLEN octets]
	//

	length := 1
	N := len(c.Certs)
	encoded := make([][]byte, N)

	for i, entry := range c.Certs {
		der, err := entry.Cert.MarshalDER()
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal certificate as DER")
		}
		encoded[i] = der
		length += 3 + len(der)
	}

	cell := NewCellEmptyPayload(f, 0, Certs, uint16(length))
	payload := cell.Payload()

	payload[0] = byte(N)
	ptr := uint16(1)

	for i, entry := range c.Certs {
		payload[ptr] = byte(entry.Type)
		ptr += 1

		der := encoded[i]
		clen := uint16(len(der))
		binary.BigEndian.PutUint16(payload[ptr:], clen)
		ptr += 2

		copied := copy(payload[ptr:], der)
		if copied != int(clen) {
			panic("incomplete copy")
		}
		ptr += clen
	}

	return cell, nil
}
