package frontconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/beevik/etree"
	"golang.org/x/text/encoding/unicode"
)

const xsiNamespace = "http://www.w3.org/2001/XMLSchema-instance"

type Field struct {
	ID          int
	Name        string
	Value       string
	Boolean     bool
	Null        bool
	Nullable    bool
	Description string
}

type Change struct {
	ID    int
	Value string
	Null  bool
}

type Document struct {
	doc      *etree.Document
	original []byte
	encoding string
	Fields   []Field
}

type entry struct {
	field   Field
	element *etree.Element
	nilKey  string
}

func Parse(data []byte) (*Document, error) {
	decoded, encoding, err := decode(data)
	if err != nil {
		return nil, err
	}
	doc := etree.NewDocument()
	doc.ReadSettings.ValidateInput = true
	doc.ReadSettings.PreserveCData = true
	// UTF-16 уже преобразован до разбора; прочие кодировки явно отклоняем.
	doc.ReadSettings.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || (strings.HasPrefix(encoding, "utf16") && strings.EqualFold(charset, "utf-16")) {
			return input, nil
		}
		return nil, fmt.Errorf("неподдерживаемая кодировка config.xml: %s", charset)
	}
	if err := doc.ReadFromBytes(decoded); err != nil {
		return nil, fmt.Errorf("не удалось разобрать config.xml: %w", err)
	}
	if len(doc.ChildElements()) != 1 || doc.Root().Tag != "config" {
		return nil, errors.New("ожидался единственный корневой элемент <config>")
	}
	d := &Document{doc: doc, original: bytes.Clone(data), encoding: encoding}
	for _, entry := range collectEntries(doc.Root()) {
		d.Fields = append(d.Fields, entry.field)
	}
	if len(d.Fields) == 0 {
		return nil, errors.New("в config.xml нет параметров для редактирования")
	}
	slices.SortStableFunc(d.Fields, func(a, b Field) int {
		return priority(a.Name) - priority(b.Name)
	})
	return d, nil
}

func priority(name string) int {
	switch name {
	case "AllowHandCardRoll":
		return 0
	case "ShowMinimizeButton":
		return 1
	case "serverUrl":
		return 2
	default:
		return 3
	}
}

func description(name string) string {
	switch name {
	case "AllowHandCardRoll":
		return "Вход по ПИН-коду вместо прокатки карты; требуется соответствующее право."
	case "ShowMinimizeButton":
		return "Показывать кнопку сворачивания окна фронта."
	case "serverUrl":
		return "Адрес и порт сервера, включая путь /resto."
	default:
		return ""
	}
}

func collectEntries(root *etree.Element) []entry {
	var entries []entry
	var walk func(*etree.Element, string)
	walk = func(parent *etree.Element, prefix string) {
		counts, seen := map[string]int{}, map[string]int{}
		for _, el := range parent.ChildElements() {
			counts[el.FullTag()]++
		}
		for _, el := range parent.ChildElements() {
			tag := el.FullTag()
			name := prefix + tag
			seen[tag]++
			if counts[tag] > 1 {
				name += "[" + strconv.Itoa(seen[tag]) + "]"
			}
			if len(el.ChildElements()) > 0 {
				walk(el, name+"/")
				continue
			}
			var text strings.Builder
			for _, child := range el.Child {
				if data, ok := child.(*etree.CharData); ok {
					text.WriteString(data.Data)
				}
			}
			value := text.String()
			field := Field{ID: len(entries), Name: name, Value: value, Description: description(name)}
			var nilKey string
			for _, attr := range el.Attr {
				if attr.Key == "nil" && attr.NamespaceURI() == xsiNamespace {
					nilKey = attr.FullKey()
					field.Nullable = true
					field.Null = attr.Value == "true" || attr.Value == "1"
				}
			}
			field.Boolean = !field.Null && (strings.EqualFold(strings.TrimSpace(value), "true") || strings.EqualFold(strings.TrimSpace(value), "false"))
			entries = append(entries, entry{field: field, element: el, nilKey: nilKey})
		}
	}
	walk(root, "")
	return entries
}

func (d *Document) Marshal(changes []Change) ([]byte, error) {
	doc := d.doc.Copy()
	entries := collectEntries(doc.Root())
	seen := map[int]bool{}
	changed := false
	for _, change := range changes {
		if change.ID < 0 || change.ID >= len(entries) || seen[change.ID] {
			return nil, errors.New("неверный или повторяющийся идентификатор параметра")
		}
		seen[change.ID] = true
		e := entries[change.ID]
		if change.Value == e.field.Value && change.Null == e.field.Null {
			continue
		}
		if change.Null && !e.field.Nullable {
			return nil, fmt.Errorf("%s не поддерживает xsi:nil", e.field.Name)
		}
		if e.field.Boolean && change.Value != "true" && change.Value != "false" && !change.Null {
			return nil, fmt.Errorf("%s: требуется true или false", e.field.Name)
		}
		if !validXMLText(change.Value) {
			return nil, fmt.Errorf("%s: значение содержит недопустимые для XML символы", e.field.Name)
		}
		changed = true
		if e.nilKey != "" {
			e.element.RemoveAttr(e.nilKey)
		}
		// Сохраняем комментарии и инструкции, заменяя только текст элемента.
		for _, child := range slices.Clone(e.element.Child) {
			if _, ok := child.(*etree.CharData); ok {
				e.element.RemoveChild(child)
			}
		}
		if change.Null {
			e.element.CreateAttr(e.nilKey, "true")
		} else {
			e.element.SetText(change.Value)
		}
	}
	if !changed {
		return bytes.Clone(d.original), nil
	}
	data, err := doc.WriteToBytes()
	if err != nil {
		return nil, fmt.Errorf("сериализация config.xml: %w", err)
	}
	switch d.encoding {
	case "utf8bom":
		data = append([]byte{0xef, 0xbb, 0xbf}, data...)
	case "utf16le":
		data, err = unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes(data)
	case "utf16be":
		data, err = unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewEncoder().Bytes(data)
	}
	if err != nil {
		return nil, fmt.Errorf("кодирование config.xml: %w", err)
	}
	if _, err := Parse(data); err != nil {
		return nil, fmt.Errorf("проверка изменённого XML: %w", err)
	}
	return data, nil
}

func validXMLText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !(r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0xd7ff || r >= 0xe000 && r <= 0xfffd || r >= 0x10000 && r <= 0x10ffff) {
			return false
		}
	}
	return true
}

func decode(data []byte) ([]byte, string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		decoded, err := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().Bytes(data)
		return decoded, "utf16le", err
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		decoded, err := unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM).NewDecoder().Bytes(data)
		return decoded, "utf16be", err
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		return data[3:], "utf8bom", nil
	default:
		return data, "utf8", nil
	}
}
