package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"go/format"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

func main() {
	if err := generateElements(); err != nil {
		log.Fatalf("%v", err)
	}
	if err := generateTags(); err != nil {
		log.Fatalf("%v", err)
	}
}

////////////////////////////////////////////////////////////////////////////////
// Elements
////////////////////////////////////////////////////////////////////////////////

type ElementsTable struct {
	XMLName  xml.Name             `xml:"EBMLSchema"`
	Elements []*EBMLSchemaElement `xml:"element"`
}

type EBMLSchemaElement struct {
	Name        string `xml:"name,attr"`
	ID          string `xml:"id,attr"`
	Type        string `xml:"type,attr"`
	Path        string `xml:"path,attr"`
	Deprecated  bool   `xml:"-"`
	IsRoot      bool   `xml:"-"`
	Restriction struct {
		Enums []*EBMLSchemaEnum `xml:"enum"`
	} `xml:"restriction"`
	Descendants []struct {
		Path string
		Name string
	} `xml:"-"`
}

type EBMLSchemaEnum struct {
	Value string `xml:"value,attr"`
	Label string `xml:"label,attr"`
	Name  string
	Type  string
}

var pathCountCleanRE = regexp.MustCompile(`\d*\*\d*\(|\(|\)`)
var pathRE = regexp.MustCompile(`\\(\(\d*-\d*\\\))?(.*)`)

func generateElements() error {
	var elements []*EBMLSchemaElement
	haveElement := map[string]bool{}
	for _, schema := range []string{
		"https://raw.githubusercontent.com/ietf-wg-cellar/ebml-specification/master/ebml.xml",
		// "https://raw.githubusercontent.com/ietf-wg-cellar/matroska-specification/master/ebml_matroska.xml",
		"https://raw.githubusercontent.com/ietf-wg-cellar/matroska-specification/v03/ebml_matroska.xml",
	} {
		isLegacySchema := strings.HasSuffix(schema, "ebml_matroska.xml")
		sb, err := loadSchema(schema)
		if err != nil {
			return err
		}
		defer sb.Close()
		data, err := ioutil.ReadAll(sb)
		if err != nil {
			return err
		}
		table := ElementsTable{}
		err = xml.Unmarshal(data, &table)
		if err != nil {
			return err
		}
		for _, el := range table.Elements {
			if _, ok := haveElement[el.Name]; ok {
				continue
			}
			haveElement[el.Name] = true
			if isLegacySchema {
				el.Path = pathCountCleanRE.ReplaceAllString(el.Path, "")
			}

			var enums []*EBMLSchemaEnum
			enumNames := map[string]struct{}{}
			for i, e := range el.Restriction.Enums {
				e.Name = camelCase(e.Label)
				if e.Name == "Reserved" {
					e.Name = fmt.Sprintf("Reserved%d", i)
				}
				if _, ok := enumNames[e.Name]; ok {
					continue
				}
				if el.Type == "string" {
					e.Type = "string"
					e.Value = fmt.Sprintf("\"%s\"", e.Value)
				} else {
					e.Type = "int64"
				}
				enums = append(enums, e)
				enumNames[e.Name] = struct{}{}
			}
			el.Restriction.Enums = enums

			elements = append(elements, el)
		}
	}

	// Add legacy named fields
	// elements = append(elements, []*EBMLSchemaElement{
	// 	{Name: "ChapterTrackNumber", ID: "ChapterTrackUIDElement", Deprecated: true, Type: "uinteger"},
	// 	{Name: "ReferenceTimeCode", ID: "ReferenceTimestampElement", Deprecated: true, Type: "uinteger"},
	// 	{Name: "TimeCode", ID: "TimestampElement", Deprecated: true, Type: "uinteger"},
	// 	{Name: "TimeCodeScale", ID: "TimestampScaleElement", Deprecated: true, Type: "uinteger"},
	// 	{Name: "TrackTimeCodeScale", ID: "TrackTimestampScaleElement", Deprecated: true, Type: "float"},
	// }...)

	log.Printf("Generating elements.go ...")

	for _, v := range elements {
		v.Name = elementName(v.Name)
		for _, del := range elements {
			if strings.Count(v.Path, "\\") == 1 {
				v.IsRoot = true
			}
			if isDescendantPath(del.Path, v.Path) {
				v.Descendants = append(v.Descendants, struct {
					Path string
					Name string
				}{del.Path, elementName(del.Name)})
			}
		}
	}
	sort.Slice(elements, func(i, j int) bool {
		return strings.Compare(elements[i].Name+"Element", elements[j].Name+"Element") < 0
	})

	var buf bytes.Buffer
	if err := elementsTemplate.Execute(&buf, elements); err != nil {
		return err
	}

	// log.Printf("Pre-format: %s", buf.String())
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return err
	}

	return os.WriteFile("elements.go", formatted, 0644)
}

func elementName(n string) string {
	return strings.Replace(n, "-", "", -1)
}

func isDescendantPath(p1, p2 string) bool {
	if p1 == p2 {
		return false
	}
	m1 := pathRE.FindStringSubmatch(p1)
	if m1 == nil {
		panic(fmt.Sprintf("unable to match path: %v", p1))
	}
	if m1[1] != "" {
		return true
	}

	m2 := pathRE.FindStringSubmatch(p2)
	if m2 == nil {
		panic(fmt.Sprintf("unable to match path: %v", p2))
	}
	return strings.HasPrefix(m1[2], m2[2])
}

var elementsTemplate = template.Must(template.New("").Parse(`// Code generated by generate.go.  DO NOT EDIT.

package mkvparse

// Supported ElementIDs
const (
	{{- range . }}
	{{ .Name }}Element ElementID = {{ .ID -}} {{- if .Deprecated -}}// Deprecated. Do not use.{{- end -}}
	{{end }}
)

func getElementType(el ElementID) elementType {
	switch (el) {
		{{- range . -}}
		{{- if not .Deprecated }}
		case {{ .Name }}Element:
		{{- if eq .Type "master" }}
			return masterType
		{{- else if eq .Type "uinteger" }}
			return uintegerType
		{{- else if eq .Type "integer" }}
			return integerType
		{{- else if eq .Type "binary" }}
			return binaryType
		{{- else if eq .Type "utf-8" }}
			return utf8Type
		{{- else if eq .Type "string" }}
			return stringType
		{{- else if eq .Type "float" }}
			return floatType
		{{- else if eq .Type "date" }}
			return dateType
		{{- end -}}
		{{ end -}}
		{{ end }}
		default:
			return elementType(0)
	}
}

var elementNames = map[ElementID]string {
	{{- range . }}
	{{- if not .Deprecated }}
	{{ .Name }}Element: {{ printf "%q" .Name }},
	{{- end -}}
	{{- end }}
}

func isDescendantElement(p1, p2 ElementID) bool {
	switch (p2) {
		{{ range . -}}
		{{ if eq .Type "master" -}}
		case {{ .Name }}Element: // {{ .Path }}
			switch(p1) {
				{{ range .Descendants -}}
				case {{ .Name }}Element: // {{ .Path }}
					return true
				{{ end -}}
				default:
					return false
			}
		{{ end -}}
		{{ end -}}
		default:
			return false
	}
}

func isRootElement(el ElementID) bool {
	switch (el) {
		{{ range . -}}
		{{ if .IsRoot -}}
			case {{ .Name }}Element: // {{ .Path }}
					return true
		{{ end -}}
		{{ end -}}
		default:
			return false
	}
}
{{- range . -}}
{{- if .Restriction.Enums }}
// Possible {{ .Name}}Element values
const (
	{{- $prefix := .Name -}}
	{{- range .Restriction.Enums }}
	// {{.Label}}
	{{$prefix}}_{{.Name}} {{.Type}} = {{.Value}}
	{{ end -}}
)
{{ end -}}
{{ end -}}
`))

////////////////////////////////////////////////////////////////////////////////
// Tags
////////////////////////////////////////////////////////////////////////////////

type Tag struct {
	Name   string `xml:"name,attr"`
	GoName string `xml:"-"`
}

type TagRegistry struct {
	XMLName xml.Name `xml:"matroska_tagging_registry"`
	Tags    *struct {
		Tags []*Tag `xml:"tag"`
	} `xml:"tags"`
}

func generateTags() error {
	sb, err := loadSchema("https://raw.githubusercontent.com/ietf-wg-cellar/matroska-specification/master/matroska_tags.xml")
	defer sb.Close()
	data, err := ioutil.ReadAll(sb)
	// data, err := ioutil.ReadFile("specdata.xml")
	if err != nil {
		return err
	}
	registry := TagRegistry{}
	err = xml.Unmarshal(data, &registry)
	if err != nil {
		return err
	}

	log.Printf("Generating tags.go ...")

	for _, v := range registry.Tags.Tags {
		switch v.Name {
		case "BPM", "BPS", "FPS", "IMDB", "ISBN", "ISRC", "LCCN", "MCDI", "TMDB", "TVDB", "URL":
			v.GoName = v.Name
		case "REPLAYGAIN_GAIN":
			v.GoName = "ReplayGainGain"
		case "REPLAYGAIN_PEAK":
			v.GoName = "ReplayGainPeak"
		default:
			v.GoName = strings.ReplaceAll(strings.Title(strings.ToLower(strings.ReplaceAll(v.Name, "_", " "))), " ", "")
		}
	}
	sort.Slice(registry.Tags.Tags, func(i, j int) bool {
		return strings.Compare("Tag"+registry.Tags.Tags[i].GoName, "Tag"+registry.Tags.Tags[j].GoName) < 0
	})

	var buf bytes.Buffer
	if err := tagsTemplate.Execute(&buf, registry); err != nil {
		return err
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return err
	}

	return os.WriteFile("tags.go", formatted, 0644)
}

var tagsTemplate = template.Must(template.New("").Parse(`// Code generated by generate.go.  DO NOT EDIT.

package mkvparse

// Official tags. See https://www.matroska.org/technical/tagging.html
const (
	{{- range .Tags.Tags }}
	Tag{{ .GoName }} string = "{{ .Name }}"
	{{- end }}
)
`))

////////////////////////////////////////////////////////////////////////////////

func loadSchema(schema string) (io.ReadCloser, error) {
	_, fn := path.Split(schema)
	var sb io.ReadCloser
	sb, err := os.Open(fn)
	if err == nil {
		return sb, nil
	}
	log.Printf("Downloading %s ...", schema)
	resp, err := http.Get(schema)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

var space = regexp.MustCompile(`[-./()]`)

func camelCase(text string) string {
	text = space.ReplaceAllString(text, " ")
	var gs []string
	for _, f := range strings.Fields(text) {
		if !isUpper(f) {
			f = strings.ToLower(f)
		}
		gs = append(gs, strings.Title(f))
	}
	return strings.Join(gs, "")
}

func isUpper(s string) bool {
	for _, r := range s {
		if !unicode.IsUpper(r) && unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
