package importsource

import "bytes"

// NewDocument retains an owned copy of one complete Rekordbox source document.
// Its identity is based on the exact source bytes, including unknown fields.
func NewDocument(data []byte) (Document, error) {
	if err := validateXML(data); err != nil {
		return Document{}, err
	}
	return Document{ID: DocumentID(data), XML: bytes.Clone(data)}, nil
}
