package frontconfig

import (
	"bytes"
	"strings"
	"testing"

	"github.com/beevik/etree"
	"golang.org/x/text/encoding/unicode"
)

const sampleXML = `<?xml version="1.0" encoding="utf-8"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" custom="keep">
  <!-- keep this comment -->
  <fastMenuConfig><width xsi:nil="true"/><enabled>true</enabled></fastMenuConfig>
  <allowedParsers>auth</allowedParsers><allowedParsers>barcode</allowedParsers>
  <serverUrl>https://server.example/resto</serverUrl>
  <ShowMinimizeButton>false</ShowMinimizeButton>
  <AllowHandCardRoll>true</AllowHandCardRoll>
  <unknown code="123"><![CDATA[a < b & c]]></unknown>
  <empty />
  <mixed>a<!-- preserved -->b</mixed>
</config>`

func mustDocument(t *testing.T, data []byte) *Document {
	t.Helper()
	doc, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func fieldNamed(t *testing.T, doc *Document, name string) Field {
	t.Helper()
	for _, field := range doc.Fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("missing field %s", name)
	return Field{}
}

func TestParseFieldsAndPriority(t *testing.T) {
	doc := mustDocument(t, []byte(sampleXML))
	for i, name := range []string{"AllowHandCardRoll", "ShowMinimizeButton", "serverUrl"} {
		if doc.Fields[i].Name != name {
			t.Fatalf("priority %d: %s", i, doc.Fields[i].Name)
		}
	}
	if !doc.Fields[0].Boolean || doc.Fields[2].Boolean {
		t.Fatal("incorrect boolean detection")
	}
	width := fieldNamed(t, doc, "fastMenuConfig/width")
	if !width.Null || !width.Nullable || width.Boolean {
		t.Fatalf("nil field: %+v", width)
	}
	a := fieldNamed(t, doc, "allowedParsers[1]")
	b := fieldNamed(t, doc, "allowedParsers[2]")
	if a.ID == b.ID || a.Value != "auth" || b.Value != "barcode" {
		t.Fatal("repeated elements lost identity")
	}
	if fieldNamed(t, doc, "mixed").Value != "ab" {
		t.Fatal("text around comment lost")
	}
}

func TestMarshalPreservesStructureAndEscapesValues(t *testing.T) {
	doc := mustDocument(t, []byte(sampleXML))
	changes := []Change{
		{ID: fieldNamed(t, doc, "ShowMinimizeButton").ID, Value: "true"},
		{ID: fieldNamed(t, doc, "serverUrl").ID, Value: "https://example.test/resto?a=1&b=<value>"},
		{ID: fieldNamed(t, doc, "fastMenuConfig/width").ID, Value: "3"},
		{ID: fieldNamed(t, doc, "allowedParsers[2]").ID, Value: "discount"},
		{ID: fieldNamed(t, doc, "mixed").ID, Value: "new"},
	}
	updated, err := doc.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	after := mustDocument(t, updated)
	for _, change := range changes {
		for _, field := range after.Fields {
			if field.ID == change.ID && (field.Value != change.Value || field.Null != change.Null) {
				t.Fatalf("wrong saved value for %s", field.Name)
			}
		}
	}
	for _, text := range []string{"<!-- keep this comment -->", "<!-- preserved -->", `custom="keep"`, `code="123"`, "<![CDATA[a < b & c]]>", "&amp;"} {
		if !bytes.Contains(updated, []byte(text)) {
			t.Fatalf("lost %s", text)
		}
	}
	if fieldNamed(t, after, "allowedParsers[1]").Value != "auth" || fieldNamed(t, after, "empty").Value != "" {
		t.Fatal("untouched field changed")
	}
	if fieldNamed(t, after, "fastMenuConfig/width").Nullable {
		t.Fatal("xsi:nil must be removed when assigning value")
	}
	if fieldNamed(t, doc, "ShowMinimizeButton").Value != "false" {
		t.Fatal("original document mutated")
	}
}

func TestMarshalNoChangesAndEncodings(t *testing.T) {
	for _, encoding := range []string{"utf8", "utf8bom", "utf16le", "utf16be"} {
		t.Run(encoding, func(t *testing.T) {
			data := []byte(sampleXML)
			switch encoding {
			case "utf8bom":
				data = append([]byte{0xef, 0xbb, 0xbf}, data...)
			case "utf16le", "utf16be":
				data = []byte(strings.Replace(sampleXML, "utf-8", "utf-16", 1))
				endian := unicode.LittleEndian
				if encoding == "utf16be" {
					endian = unicode.BigEndian
				}
				var err error
				data, err = unicode.UTF16(endian, unicode.UseBOM).NewEncoder().Bytes(data)
				if err != nil {
					t.Fatal(err)
				}
			}
			doc := mustDocument(t, data)
			unchanged, err := doc.Marshal(nil)
			if err != nil || !bytes.Equal(data, unchanged) {
				t.Fatal("no-op must preserve original bytes")
			}
			updated, err := doc.Marshal([]Change{{ID: fieldNamed(t, doc, "empty").ID, Value: "Привет & мир"}})
			if err != nil {
				t.Fatal(err)
			}
			after := mustDocument(t, updated)
			if after.encoding != encoding || fieldNamed(t, after, "empty").Value != "Привет & мир" {
				t.Fatal("encoding round trip failed")
			}
		})
	}
}

func TestInvalidXMLAndChanges(t *testing.T) {
	for _, input := range []string{"", "<config>", "<other/>", "<config/><config/>", "<config/>", "<config><x>&broken;</x></config>"} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("accepted invalid input %q", input)
		}
	}
	doc := mustDocument(t, []byte(sampleXML))
	boolean := fieldNamed(t, doc, "AllowHandCardRoll").ID
	for _, changes := range [][]Change{
		{{ID: -1}}, {{ID: 999}}, {{ID: boolean, Value: "yes"}},
		{{ID: boolean, Value: "false"}, {ID: boolean, Value: "true"}},
		{{ID: boolean, Null: true}},
		{{ID: fieldNamed(t, doc, "empty").ID, Value: "invalid\x00value"}},
	} {
		if _, err := doc.Marshal(changes); err == nil {
			t.Fatalf("accepted invalid changes %+v", changes)
		}
	}
}

func TestNullAndEmptyAreDistinct(t *testing.T) {
	doc := mustDocument(t, []byte(`<config xmlns:n="http://www.w3.org/2001/XMLSchema-instance"><a n:nil="true"/><b n:nil="false">value</b></config>`))
	updated, err := doc.Marshal([]Change{{ID: 0, Value: ""}, {ID: 1, Null: true}})
	if err != nil {
		t.Fatal(err)
	}
	parsed := etree.NewDocument()
	if err := parsed.ReadFromBytes(updated); err != nil {
		t.Fatal(err)
	}
	if parsed.FindElement("config/a").SelectAttr("n:nil") != nil || parsed.FindElement("config/b").SelectAttrValue("n:nil", "") != "true" || parsed.FindElement("config/b").Text() != "" {
		t.Fatal("empty and nil not correctly distinguished")
	}
}
