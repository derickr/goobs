package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"unicode"

	. "github.com/dave/jennifer/jen"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var (
	goobs = "github.com/andreykaipov/goobs"

	version  = "4.5.0"
	comments = fmt.Sprintf("https://raw.githubusercontent.com/Palakis/obs-websocket/%s/docs/generated/comments.json", version)
)

func main() {
	resp, err := http.Get(comments)
	if err != nil {
		panic(err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)

	data := &Comments{}
	if err := json.Unmarshal(body, data); err != nil {
		panic(err)
	}

	root, err := run("git rev-parse --show-toplevel")
	if err != nil {
		panic(err)
	}

	topClientFields := []Code{}
	topClientSetters := []Code{}

	/**********************/
	fmt.Println("Requests")

	for _, category := range sortedKeys(data.Requests) {
		fmt.Printf("- %s \n", category)

		categorySnake := strings.ReplaceAll(category, " ", "_")
		categoryPascal := strings.ReplaceAll(strings.Title(category), " ", "")
		categoryClaustrophic := strings.ReplaceAll(category, " ", "")

		// For the top-level client
		qualifier := goobs + "/api/requests/" + categorySnake
		topClientFields = append(topClientFields, Id(categoryPascal).Op("*").Qual(qualifier, "Client"))
		topClientSetters = append(
			topClientSetters, Id("c").Dot(categoryPascal).Op("=").Op("&").Qual(qualifier, "Client").Values(
				Id("Conn").Op(":").Id("c").Dot("requestsConn"),
			),
		)

		// Generate the category-level client
		client := NewFile(categoryClaustrophic)
		client.HeaderComment("This file has been automatically generated. Don't edit it.")
		client.Commentf("Client represents a client for '%s' requests", category)
		client.Add(
			Type().Id("Client").Struct(
				Id("Conn").Op("*").Qual("github.com/gorilla/websocket", "Conn"),
			),
		)

		// Write the category-level client
		dir := fmt.Sprintf("%s/api/requests/%s", root, categorySnake)
		if err := os.MkdirAll(dir, 0777); err != nil {
			panic(err)
		}
		if err := client.Save(fmt.Sprintf("%s/zz_generated.client.go", dir)); err != nil {
			panic(err)
		}

		// Generate the requests for the category
		for _, request := range data.Requests[category] {
			// fmt.Printf("  - %s\n", request.Name)

			if request.Deprecated != "" {
				// fmt.Fprintf(os.Stderr, "Request %q is deprecated\n", request.Name)
				continue
			}

			s, err := generateRequest(request)
			if err != nil {
				panic(err)
			}

			f := NewFile(categoryClaustrophic)
			f.HeaderComment("This file has been automatically generated. Don't edit it.")
			f.Add(s)
			fName := strings.ToLower(request.Name)
			if err := f.Save(fmt.Sprintf("%s/xx_generated.%s.go", dir, fName)); err != nil {
				panic(err)
			}
		}
	}

	// Write utils for the top-level client
	f := NewFile("goobs")
	f.HeaderComment("This file has been automatically generated. Don't edit it.")
	f.Add(Type().Id("subclients").Struct(topClientFields...))
	f.Add(Func().Id("setClients").Params(Id("c").Op("*").Id("Client")).Block(topClientSetters...))

	if err := f.Save(fmt.Sprintf("%s/zz_generated.client.go", root)); err != nil {
		panic(err)
	}

	/**********************/
	fmt.Println("Events")
	events := []*Event{}

	for _, category := range sortedKeys(data.Events) {
		fmt.Printf("- %s\n", category)

		categorySnake := strings.ReplaceAll(category, " ", "_")

		dir := fmt.Sprintf("%s/api/events", root)
		if err := os.MkdirAll(dir, 0777); err != nil {
			panic(err)
		}

		// Generate the events for the category
		for _, event := range data.Events[category] {
			// fmt.Printf("  - %s\n", event.Name)

			s, err := generateEvent(event)
			if err != nil {
				panic(err)
			}

			f := NewFile("events")
			f.HeaderComment("This file has been automatically generated. Don't edit it.")
			f.Add(s)
			fName := strings.ToLower(event.Name)
			if err := f.Save(fmt.Sprintf("%s/zz_generated.%s.%s.go", dir, categorySnake, fName)); err != nil {
				panic(err)
			}

			events = append(events, event)
		}
	}

	f = NewFile("events")
	f.HeaderComment("This file has been automatically generated. Don't edit it.")
	f.Add(
		Func().Id("GetEventForType").Params(Id("name").String()).Id("Event").Block(
			Switch(Id("name")).BlockFunc(func(g *Group) {
				for _, e := range events {
					g.Case(Lit(e.Name))
					g.Return(Op("&").Id(e.Name).Values())
				}
				g.Default().Return(Nil()) //Op("&").Qual(goobs+"/api/events", "EventCommon").Values())
			}),
		),
	)
	dir := fmt.Sprintf("%s/api/events", root)
	if err := os.MkdirAll(dir, 0777); err != nil {
		panic(err)
	}
	if err := f.Save(fmt.Sprintf("%s/xx_generated.events.go", dir)); err != nil {
		panic(err)
	}
}

func generateRequest(request *Request) (s *Statement, err error) {
	var structName string
	s = Line()
	note := fmt.Sprintf("Generated from https://github.com/Palakis/obs-websocket/blob/%s/docs/generated/protocol.md#%s.", version, request.Name)

	// Params
	structName = request.Name + "Params"
	s.Commentf("%s represents the params body for the %q request.\n\n%s", structName, request.Name, note).Line()
	request.Params = append(request.Params, &Param{Name: "ParamsBasic", Type: "~requests~"}) // internal type
	if err = generateStructFromParams(s, structName, request.Params); err != nil {
		return nil, fmt.Errorf("Failed parsing 'Params' for request %q in category %q", request.Name, request.Category)
	}

	s.Add(
		Commentf("Name just returns %q.", request.Name).Line(),
		Func().Params(Id("o").Op("*").Id(structName)).Id("Name").Params().String().Block(
			Return(Lit(request.Name)),
		).Line(),
	)

	// Returns
	structName = request.Name + "Response"
	s.Commentf("%s represents the response body for the %q request.\n\n%s", structName, request.Name, note).Line()
	request.Returns = append(request.Returns, &Param{Name: "ResponseBasic", Type: "~requests~"}) // internal type
	if err = generateStructFromParams(s, structName, request.Returns); err != nil {
		return nil, fmt.Errorf("Failed parsing 'Returns' for request %q in category %q", request.Name, request.Category)
	}

	// generate the request function

	hasRequiredArgs := len(request.Params) > 1

	s.Commentf("%s sends the corresponding request to the connected OBS WebSockets server.", request.Name).Do(func(z *Statement) {
		if hasRequiredArgs {
			return
		}
		z.Id("Note the variadic arguments as this request doesn't require any parameters.")
	})
	s.Line()
	s.Func().Params(Id("c").Op("*").Id("Client")).Id(request.Name).ParamsFunc(func(g *Group) {
		if hasRequiredArgs {
			g.Id("params").Op("*").Id(request.Name + "Params")
		} else {
			g.Id("paramss").Op("...").Op("*").Id(request.Name + "Params")
		}
	}).Params(Op("*").Id(request.Name+"Response"), Error()).Block(
		Do(func(z *Statement) {
			if hasRequiredArgs {
				return
			}
			z.If(Len(Id("paramss")).Op("==").Lit(0)).Block(
				Id("paramss").Op("=").Index().Op("*").Id(request.Name + "Params").Values(Values()),
			)
			z.Line()
			z.Id("params").Op(":=").Id("paramss").Index(Lit(0))
		}),
		Id("data").Op(":=").Op("&").Id(request.Name+"Response").Values(),
		If(
			Id("err").Op(":=").Qual(goobs+"/api/requests", "WriteMessage").Call(
				Id("c").Dot("Conn"), Id("params"), Id("data"),
			),
			Id("err").Op("!=").Nil(),
		).Block(
			Return().List(Nil(), Id("err")),
		),
		Return().List(Id("data"), Nil()),
	)

	return s, nil
}

func generateEvent(event *Event) (s *Statement, err error) {
	s = Line()
	note := fmt.Sprintf("Generated from https://github.com/Palakis/obs-websocket/blob/%s/docs/generated/protocol.md#%s.", version, event.Name)

	s.Commentf("%s represents the event body for the %q event.\n\n%s", event.Name, event.Name, note).Line()
	event.Returns = append(event.Returns, &Param{Name: "EventBasic", Type: "~events~"}) // internal type
	if err = generateStructFromParams(s, event.Name, event.Returns); err != nil {
		return nil, fmt.Errorf("Failed generating event %q in category %q", event.Name, event.Category)
	}

	return s, nil
}

func generateStructFromParams(s *Statement, name string, params []*Param) error {
	keysInfo := map[string]keyInfo{}

	for _, field := range params {
		fieldName, err := sanitizeText(field.Name)
		if err != nil {
			return fmt.Errorf("Failed sanitizing field %#v", field)
		}

		noJSONTag := false
		embedded := false

		var fieldType *Statement
		switch strings.Trim(strings.ReplaceAll(field.Type, "(optional)", ""), " ") {
		case "string":
			fieldType = String()
		case "String":
			fieldType = String()
		case "int":
			fieldType = Int()
		case "Integer":
			fieldType = Int()
		case "double":
			fieldType = Float64()
		case "float":
			fieldType = Float64()
		case "bool":
			fieldType = Bool()
		case "boolean":
			fieldType = Bool()
		case "Boolean":
			fieldType = Bool()
		case "Object":
			fieldType = Map(String()).Interface()
		case "Array<String>":
			fieldType = Index().String()
		case "Array<Object>":
			fieldType = Index().Map(String()).Interface()
		case "Array<Source>":
			fieldType = Index().Map(String()).Interface()
		case "Array<Scene>":
			fieldType = Index().Map(String()).Interface()
		case "Scene|Array":
			fieldType = Index().Map(String()).Interface()
		case "~requests~":
			fieldType = Qual(goobs+"/api/requests", field.Name)
			embedded = true
		case "~events~":
			fieldType = Id(field.Name)
			embedded = true
		default:
			panic(fmt.Errorf("%s is a weird type", field.Name))
		}

		// TODO remove in 4.9.0
		if strings.Contains(fieldName, "position") {
			fieldType = Float64()
		}

		keysInfo[fieldName] = keyInfo{
			Type:      fieldType,
			Comment:   field.Description,
			NoJSONTag: noJSONTag,
			Embedded:  embedded,
		}
	}

	statement, err := parseJenKeysAsStruct(name, keysInfo)
	if err != nil {
		return fmt.Errorf("Failed parsing dotted key: %s", err)
	}

	s.Add(statement)

	return nil
}

func generateGetter(strukt, name string) *Statement {
	f := "Get" + strings.Title(name)

	s := Commentf("%s returns the %s of %s", f, name, strukt).Line()

	s.Func().Params(Id("o").Op("*").Id(strukt)).Id("Get" + name).Params().String().Block(
		Return(Id("o").Dot(name)),
	).Line()

	return s
}

func generateSetter(strukt, name string) *Statement {
	f := "Set" + strings.Title(name)

	s := Commentf("%s sets the %s on %s", f, name, strukt).Line()

	s.Func().Params(Id("o").Op("*").Id(strukt)).Id(f).Params(Id("x").String()).Block(
		Id("o").Dot(name).Op("=").Id("x"),
	).Line()

	return s
}

func sanitizeText(text string) (string, error) {
	isMn := func(r rune) bool {
		// Mn: nonspacing marks
		return unicode.Is(unicode.Mn, r)
	}

	t := transform.Chain(norm.NFD, transform.RemoveFunc(isMn), norm.NFC)
	clean, _, err := transform.String(t, text)
	return clean, err
}

func run(cmd string) (string, error) {
	output, err := exec.Command("/bin/sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Failed running '%s': %s\n\n%s", cmd, err, output)
	}

	return strings.TrimSuffix(string(output), "\n"), nil
}

func sortedKeys(m interface{}) []string {
	keys := reflect.ValueOf(m).MapKeys()

	sorted := make([]string, len(keys))
	i := 0
	for _, key := range keys {
		sorted[i] = key.Interface().(string)
		i++
	}
	sort.Strings(sorted)

	return sorted
}
