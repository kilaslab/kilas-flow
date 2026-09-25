package n8n

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

// n8n keeps a workflow as JSON it never checks against a schema, so an export
// can carry a number written as a string — `"typeVersion": "3.4"` — and n8n
// loads and runs it, because its engine reads those fields through
// JavaScript's own coercion. Decoding straight into float64 and bool refused
// the whole file over one such value, with a Go message that named a struct
// the user has never heard of (BUG-w18vn3).
//
// So the node's numeric fields accept a number or a string holding one, which
// is what n8n reads there, and its flags are on only when they are literally
// true, which is the test n8n's engine applies. A value n8n could not have
// read as a number either does not refuse the file: the node imports as
// though the field were absent, and the importer names the field and the
// value it could not read.

// unreadableField is one node field whose value could not be read as the
// number n8n reads it as.
type unreadableField struct {
	field string
	// value is the JSON exactly as the export wrote it, so a reason can quote
	// it — `"latest"`, with its quotes, says it was a string.
	value string
}

// UnmarshalJSON reads one n8n node, tolerating what n8n tolerates.
func (node *Node) UnmarshalJSON(data []byte) error {
	// The named copy has Node's fields and none of its methods, so decoding
	// into it does not come back here. The raw fields sit at a shallower
	// depth than the copy's, which is what makes encoding/json fill them
	// instead of the typed ones they shadow.
	type fields Node
	var shadow struct {
		fields
		TypeVersion      json.RawMessage `json:"typeVersion"`
		Position         json.RawMessage `json:"position"`
		MaxTries         json.RawMessage `json:"maxTries"`
		WaitBetweenTries json.RawMessage `json:"waitBetweenTries"`
		Disabled         json.RawMessage `json:"disabled"`
		ContinueOnFail   json.RawMessage `json:"continueOnFail"`
		RetryOnFail      json.RawMessage `json:"retryOnFail"`
		AlwaysOutputData json.RawMessage `json:"alwaysOutputData"`
		ExecuteOnce      json.RawMessage `json:"executeOnce"`
	}
	if err := json.Unmarshal(data, &shadow); err != nil {
		subject := "a node"
		if strings.TrimSpace(shadow.Name) != "" {
			subject = fmt.Sprintf("node %q", shadow.Name)
		}
		return plainDecodeError(subject, err)
	}

	*node = Node(shadow.fields)
	node.unreadable = nil
	node.TypeVersion = node.looseNumber("typeVersion", shadow.TypeVersion)
	node.MaxTries = node.looseNumber("maxTries", shadow.MaxTries)
	node.WaitBetweenTries = node.looseNumber("waitBetweenTries", shadow.WaitBetweenTries)
	node.Position = node.loosePosition(shadow.Position)
	node.Disabled = literallyTrue(shadow.Disabled)
	node.ContinueOnFail = literallyTrue(shadow.ContinueOnFail)
	node.RetryOnFail = literallyTrue(shadow.RetryOnFail)
	node.AlwaysOutputData = literallyTrue(shadow.AlwaysOutputData)
	node.ExecuteOnce = literallyTrue(shadow.ExecuteOnce)
	return nil
}

// looseNumber reads a numeric node field, recording a value it cannot read.
// An unreadable value reads as zero, which every caller already treats as
// "the export did not say".
func (node *Node) looseNumber(field string, raw json.RawMessage) float64 {
	value, readable := numberFrom(raw)
	if !readable {
		node.unreadable = append(node.unreadable, unreadableField{field: field, value: string(bytes.TrimSpace(raw))})
		return 0
	}
	return value
}

// loosePosition reads the canvas position. It is all or nothing: a position
// with one unreadable coordinate would put the node somewhere nobody chose.
func (node *Node) loosePosition(raw json.RawMessage) []float64 {
	if absent(raw) {
		return nil
	}
	var coordinates []json.RawMessage
	if err := json.Unmarshal(raw, &coordinates); err == nil {
		position := make([]float64, 0, len(coordinates))
		readable := true
		for _, coordinate := range coordinates {
			value, ok := numberFrom(coordinate)
			if !ok || absent(coordinate) {
				readable = false
				break
			}
			position = append(position, value)
		}
		if readable {
			return position
		}
	}
	node.unreadable = append(node.unreadable, unreadableField{field: "position", value: string(bytes.TrimSpace(raw))})
	return nil
}

// numberFrom reads a JSON number, or a string holding one. An absent value or
// null is readable and zero; so is nothing else that is not a number.
func numberFrom(raw json.RawMessage) (float64, bool) {
	if absent(raw) {
		return 0, true
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false
	}
	return number, true
}

func absent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// literallyTrue is n8n's own test for a node flag: its engine asks whether
// the value is `=== true`, so the string "true" leaves the flag off there,
// and reading it as on here would switch on behaviour the workflow never had.
func literallyTrue(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("true"))
}

// UnmarshalJSON reads one connection target. n8n indexes an array with the
// value, and JavaScript reads "0" there exactly as it reads 0.
func (target *Target) UnmarshalJSON(data []byte) error {
	type fields Target
	var shadow struct {
		fields
		Index json.RawMessage `json:"index"`
	}
	if err := json.Unmarshal(data, &shadow); err != nil {
		return plainDecodeError("a connection", err)
	}
	*target = Target(shadow.fields)
	index, readable := numberFrom(shadow.Index)
	if !readable || index != math.Trunc(index) || math.Abs(index) > math.MaxInt32 {
		// Refused rather than guessed: the index says which input the edge
		// lands on, and a wrong guess wires the workflow differently.
		return fmt.Errorf("the connection to %q gives its input index as %s, which is not a whole number", target.Node, bytes.TrimSpace(shadow.Index))
	}
	target.Index = int(index)
	return nil
}

// plainDecodeError rewords a value of the wrong JSON kind in the terms of the
// file the user holds. encoding/json names the Go type it decodes into, which
// says nothing to somebody holding an n8n export.
func plainDecodeError(subject string, err error) error {
	var mistyped *json.UnmarshalTypeError
	if !errors.As(err, &mistyped) {
		return err
	}
	if mistyped.Field == "" {
		if subject == "the workflow" && mistyped.Value == "array" {
			// `n8n export:workflow --all` writes every workflow into one list.
			return fmt.Errorf("the file is a list of workflows; import each workflow on its own")
		}
		return fmt.Errorf("%s is %s, where n8n writes %s", subject, jsonKindPhrase(mistyped.Value), goKindPhrase(mistyped.Type))
	}
	return fmt.Errorf("%s has a %q that is %s, where n8n writes %s", subject, mistyped.Field, jsonKindPhrase(mistyped.Value), goKindPhrase(mistyped.Type))
}

// jsonKindPhrase names the JSON kind encoding/json reports it found. A
// number's report carries its text ("number 1.5"), which reads fine after
// the article.
func jsonKindPhrase(value string) string {
	switch value {
	case "array":
		return "a list"
	case "object":
		return "an object"
	case "bool":
		return "true or false"
	default:
		return "a " + value
	}
}

// goKindPhrase names, in JSON's terms, what the decoder wanted.
func goKindPhrase(target reflect.Type) string {
	if target == nil {
		return "something else"
	}
	switch target.Kind() {
	case reflect.Map, reflect.Struct:
		return "an object"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	default:
		return "something else"
	}
}

// unreadableIssues names each node field that could not be read, and what the
// import did instead.
func unreadableIssues(name, id string, node Node) []ImportIssue {
	issues := make([]ImportIssue, 0, len(node.unreadable))
	for _, unreadable := range node.unreadable {
		issue := ImportIssue{
			Severity: SeverityDropped,
			NodeName: name, NodeID: id, Field: unreadable.field, Type: node.Type,
		}
		switch unreadable.field {
		case "typeVersion":
			// Lossy, not dropped: the node still lands on a version — the one
			// assumed when an export names none — and whether its parameters
			// mean the same thing there is for the author to check.
			issue.Severity = SeverityLossy
			issue.Reason = fmt.Sprintf("n8n wrote this node's typeVersion as %s, which is not a number. The node was imported as though it named no version, so check that its parameters still mean what they did.", unreadable.value)
		case "position":
			issue.Reason = fmt.Sprintf("n8n wrote this node's position as %s, which is not a pair of numbers, so the node was placed at the canvas origin.", unreadable.value)
		default:
			issue.Reason = fmt.Sprintf("n8n wrote this node's %s as %s, which is not a number, so it was not carried and the default applies.", unreadable.field, unreadable.value)
		}
		issues = append(issues, issue)
	}
	return issues
}
