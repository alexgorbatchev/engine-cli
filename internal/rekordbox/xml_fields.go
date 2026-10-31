package rekordbox

import (
	"bytes"
	"encoding/xml"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/nateranda/djtools/rbxml"
)

// The maintained parser's XML tags define its recognized shape. Unknown data
// remains in the source archive and requires consent rather than being silently
// discarded by encoding/xml's normal unmatched-field handling.
func hasUnmappedXML(data []byte) (bool, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	stack := []reflect.Type{}
	unknown := false
	for {
		token, err := d.Token()
		if err == io.EOF {
			return unknown, nil
		}
		if err != nil {
			return false, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			t := reflect.TypeFor[rbxml.Document]()
			if len(stack) > 0 {
				t = xmlFieldType(stack[len(stack)-1], token.Name, false)
			}
			if t == nil {
				unknown = true
				if err := d.Skip(); err != nil {
					return false, err
				}
				continue
			}
			for _, attr := range token.Attr {
				unknown = unknown || xmlFieldType(t, attr.Name, true) == nil
			}
			stack = append(stack, t)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			unknown = unknown || len(bytes.TrimSpace(token)) != 0
		}
	}
}

func xmlFieldType(t reflect.Type, name xml.Name, attribute bool) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if name.Space != "" || t.Kind() != reflect.Struct {
		return nil
	}
	for i := range t.NumField() {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("xml"), ",")
		if field.Name == "XMLName" || tag[0] != name.Local {
			continue
		}
		if attribute == slices.Contains(tag[1:], "attr") {
			return field.Type
		}
	}
	return nil
}
