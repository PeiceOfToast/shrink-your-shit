// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// DecodeSettings turns one step's saved settings JSON into the step's own settings struct.
//
// The field names are the struct's set: tags. An absent field takes its default: value; a null
// only fits an optional (pointer) field, and stays nil; a name the struct does not have is refused.
// The choices and ranges in the tags are checked here as well, because an imported or upgraded
// flow may be out of range (step-interface.md: checked on save and again at run time).
func DecodeSettings(def Definition, raw []byte) (Settings, error) {
	typ := reflect.TypeOf(def.Settings)
	if typ == nil || typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("flow: %s has no settings struct", def.Type)
	}
	v := reflect.New(typ).Elem()
	present := make(map[string]bool)

	if text := bytes.TrimSpace(raw); len(text) > 0 && !bytes.Equal(text, []byte("null")) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(text, &fields); err != nil {
			return nil, fmt.Errorf("%s settings: %w", def.Type, err)
		}
		for _, name := range sortedKeys(fields) {
			index, ok := settingsField(typ, name)
			if !ok {
				return nil, fmt.Errorf("%s settings: %q is not a setting", def.Type, name)
			}
			if err := setField(v.Field(index), name, fields[name]); err != nil {
				return nil, fmt.Errorf("%s settings: %w", def.Type, err)
			}
			present[name] = true
		}
	}
	if err := applyDefaults(v, present); err != nil {
		return nil, fmt.Errorf("%s settings: %w", def.Type, err)
	}
	s := v.Interface()
	if problems := fieldProblems(def, s); len(problems) > 0 {
		msgs := make([]string, 0, len(problems))
		for _, p := range problems {
			msgs = append(msgs, p.Message)
		}
		return nil, fmt.Errorf("%s settings: %s", def.Type, strings.Join(msgs, " "))
	}
	return s, nil
}

// settingsField is the struct field index whose set: tag is name.
func settingsField(typ reflect.Type, name string) (int, bool) {
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Tag.Get("set") == name {
			return i, true
		}
	}
	return 0, false
}

// sortedKeys keeps a decode error naming the same field every time, whatever map order Go used.
func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// setField fills one struct field from its JSON value.
func setField(field reflect.Value, name string, raw json.RawMessage) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if field.Kind() == reflect.Pointer {
			field.SetZero()
			return nil
		}
		return fmt.Errorf("%q cannot be empty", name)
	}
	isPointer := field.Kind() == reflect.Pointer
	target := field
	if isPointer {
		target = reflect.New(field.Type().Elem()).Elem()
	}
	var err error
	switch target.Kind() {
	case reflect.String:
		var s string
		if err = json.Unmarshal(raw, &s); err != nil {
			err = fmt.Errorf("%q must be text", name)
		} else {
			target.SetString(s)
		}
	case reflect.Int:
		var n json.Number
		if err = json.Unmarshal(raw, &n); err != nil {
			err = fmt.Errorf("%q must be a whole number", name)
		} else if v, convErr := n.Int64(); convErr != nil {
			err = fmt.Errorf("%q must be a whole number", name)
		} else {
			target.SetInt(v)
		}
	case reflect.Bool:
		var b bool
		if err = json.Unmarshal(raw, &b); err != nil {
			err = fmt.Errorf("%q must be yes or no", name)
		} else {
			target.SetBool(b)
		}
	case reflect.Float64:
		var n json.Number
		if err = json.Unmarshal(raw, &n); err != nil {
			err = fmt.Errorf("%q must be a number", name)
		} else if v, convErr := n.Float64(); convErr != nil {
			err = fmt.Errorf("%q must be a number", name)
		} else {
			target.SetFloat(v)
		}
	case reflect.Slice:
		if target.Type().Elem().Kind() != reflect.String {
			err = fmt.Errorf("%q has a type this program cannot read", name)
			break
		}
		var list []string
		if err = json.Unmarshal(raw, &list); err != nil {
			err = fmt.Errorf("%q must be a list of text", name)
		} else {
			target.Set(reflect.ValueOf(list))
		}
	default:
		err = fmt.Errorf("%q has a type this program cannot read", name)
	}
	if err != nil {
		return err
	}
	if isPointer {
		field.Set(target.Addr())
	}
	return nil
}

// applyDefaults fills every field the saved JSON did not name from its default: tag. A field with
// no default stays the zero value, which means unset.
func applyDefaults(v reflect.Value, present map[string]bool) error {
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		sf := typ.Field(i)
		name := sf.Tag.Get("set")
		if name == "" || present[name] {
			continue
		}
		text, ok := sf.Tag.Lookup("default")
		if !ok {
			continue
		}
		field := v.Field(i)
		isPointer := field.Kind() == reflect.Pointer
		target := field
		if isPointer {
			target = reflect.New(field.Type().Elem()).Elem()
		}
		switch target.Kind() {
		case reflect.String:
			target.SetString(text)
		case reflect.Int:
			n, err := strconv.ParseInt(text, 10, 64)
			if err != nil {
				return fmt.Errorf("%q has a default that is not a number: %q", name, text)
			}
			target.SetInt(n)
		case reflect.Bool:
			b, err := strconv.ParseBool(text)
			if err != nil {
				return fmt.Errorf("%q has a default that is not yes or no: %q", name, text)
			}
			target.SetBool(b)
		case reflect.Float64:
			n, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return fmt.Errorf("%q has a default that is not a number: %q", name, text)
			}
			target.SetFloat(n)
		case reflect.Slice:
			if target.Type().Elem().Kind() != reflect.String {
				return fmt.Errorf("%q has a default this program cannot read", name)
			}
			if text == "" {
				target.SetZero()
			} else {
				target.Set(reflect.ValueOf(strings.Split(text, ",")))
			}
		default:
			return fmt.Errorf("%q has a default this program cannot read", name)
		}
		if isPointer {
			field.Set(target.Addr())
		}
	}
	return nil
}

// fieldProblems is the tags' part of Validate: the choices and ranges every settings shape must
// keep, whether it came from the form, an import or an upgrade. DecodeSettings runs it as well.
func fieldProblems(def Definition, s Settings) []Problem {
	v := reflect.ValueOf(s)
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return nil
	}
	typ := v.Type()
	var out []Problem
	for i := 0; i < typ.NumField(); i++ {
		sf := typ.Field(i)
		name := sf.Tag.Get("set")
		if name == "" {
			continue
		}
		field := v.Field(i)
		if field.Kind() == reflect.Pointer {
			if field.IsNil() {
				continue
			}
			field = field.Elem()
		}
		label := sf.Tag.Get("label")
		if label == "" {
			label = name
		}
		switch field.Kind() {
		case reflect.String:
			if list := choiceValues(sf.Tag.Get("choices")); len(list) > 0 && !slices.Contains(list, field.String()) {
				out = append(out, Problem{
					Field: name, Code: "setting.choice",
					Message: fmt.Sprintf("%s must be %s.", label, humanList(choiceLabels(sf.Tag.Get("choices")))),
				})
			}
		case reflect.Int:
			n := field.Int()
			if text, ok := sf.Tag.Lookup("min"); ok {
				if min, err := strconv.ParseInt(text, 10, 64); err == nil && n < min {
					out = append(out, Problem{
						Field: name, Code: "setting.range",
						Message: fmt.Sprintf("%s must be at least %d.", label, min),
					})
				}
			}
			if text, ok := sf.Tag.Lookup("max"); ok {
				if max, err := strconv.ParseInt(text, 10, 64); err == nil && n > max {
					out = append(out, Problem{
						Field: name, Code: "setting.range",
						Message: fmt.Sprintf("%s must be at most %d.", label, max),
					})
				}
			}
			if list := choiceNumbers(sf.Tag.Get("choices")); len(list) > 0 && !slices.Contains(list, n) {
				out = append(out, Problem{
					Field: name, Code: "setting.choice",
					Message: fmt.Sprintf("%s must be %s.", label, humanList(choiceLabels(sf.Tag.Get("choices")))),
				})
			}
		case reflect.Float64:
			n := field.Float()
			min, minOK := floatTag(sf, "min")
			max, maxOK := floatTag(sf, "max")
			if minOK && n < min {
				out = append(out, Problem{
					Field: name, Code: "setting.range",
					Message: fmt.Sprintf("%s must be at least %s.", label, numberText(min)),
				})
			}
			if maxOK && n > max {
				out = append(out, Problem{
					Field: name, Code: "setting.range",
					Message: fmt.Sprintf("%s must be at most %s.", label, numberText(max)),
				})
			}
		}
	}
	return out
}

// choice is one value a setting accepts and the words the page shows for it. The tag is
// "value", "value=Label" for a choice whose words differ from its value, joined by commas.
type choice struct {
	Value string
	Label string
}

// parseChoices reads a choices: tag. A malformed pair keeps the value as its label.
func parseChoices(tag string) []choice {
	if tag == "" {
		return nil
	}
	items := strings.Split(tag, ",")
	out := make([]choice, 0, len(items))
	for _, item := range items {
		value, label, _ := strings.Cut(strings.TrimSpace(item), "=")
		value = strings.TrimSpace(value)
		label = strings.TrimSpace(label)
		if value == "" {
			continue
		}
		if label == "" {
			label = value
		}
		out = append(out, choice{Value: value, Label: label})
	}
	return out
}

func choiceValues(tag string) []string {
	choices := parseChoices(tag)
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, c.Value)
	}
	return out
}

func choiceLabels(tag string) []string {
	choices := parseChoices(tag)
	out := make([]string, 0, len(choices))
	for _, c := range choices {
		out = append(out, c.Label)
	}
	return out
}

// choiceNumbers is the choices that are whole numbers, so a number field can check membership.
func choiceNumbers(tag string) []int64 {
	choices := parseChoices(tag)
	var out []int64
	for _, c := range choices {
		if n, err := strconv.ParseInt(c.Value, 10, 64); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// floatTag reads a whole or decimal number tag, so a number setting can carry a range.
func floatTag(sf reflect.StructField, name string) (float64, bool) {
	text, ok := sf.Tag.Lookup(name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// numberText writes a whole number without its decimal point and keeps a decimal one as typed
// ("1000", "0.1").
func numberText(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// TagProblems is the tags' part of a step's Validate, exported so the steps can run it on settings
// that did not come through DecodeSettings (a test, or a hand-built draft).
func TagProblems(def Definition, s Settings) []Problem { return fieldProblems(def, s) }

// humanList joins choices the way the pages write them: "a, b or c".
func humanList(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}
