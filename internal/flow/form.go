// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Form is one step's card for the flow editor (page 07): JSON Schema for the settings, plus the
// layout hints that let the page draw it with no per-step code. It comes from the settings
// struct's tags alone (step-interface.md, "Settings: one struct with tags"), so a field with no
// tag cannot ship without a form.
type Form struct {
	// Groups is the display order: "basic" (the card's own body) first when it has fields, then
	// the step's own sections (Advanced, Subtitles) in the order its Definition lists them.
	Groups []Group `json:"groups"`
	Schema Schema  `json:"schema"`
}

// Group is one section of a card. Basic's label is empty: the card's body has no heading.
type Group struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
}

// Schema is a JSON Schema object for one step's settings. Only the words the pages use.
type Schema struct {
	Type                 string              `json:"type"`
	AdditionalProperties bool                `json:"additionalProperties"`
	Properties           map[string]Property `json:"properties"`
}

// Property is one setting. Type is "integer" and the like, or a list with "null" first for a
// setting that can be unset. Default is always present (null when unset).
type Property struct {
	Type        any      `json:"type"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Enum        []any    `json:"enum,omitempty"`
	Default     any      `json:"default"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	Items       *Items   `json:"items,omitempty"`
	Layout      Layout   `json:"x-ui"`
}

// Items is the entry schema of a list setting.
type Items struct {
	Type string `json:"type"`
}

// Layout is the part JSON Schema has no words for: how the page draws the field.
type Layout struct {
	Widget string `json:"widget"`
	Group  string `json:"group"`
	Order  int    `json:"order"`
	Unit   string `json:"unit,omitempty"`
	// ShowIf hides the field until another setting has the right value, e.g. Custom size's
	// width and height only with size "custom".
	ShowIf *When `json:"showIf,omitempty"`
	// Choices carries the words for each value and whether this server can do it. A field with no
	// choices is a box or a check box; the check route greys those, not the form.
	Choices []Option `json:"choices,omitempty"`
	// SliderLabels names each position of a slider, lowest first.
	SliderLabels []string `json:"sliderLabels,omitempty"`
}

// Option is one value a setting accepts: the value itself, the page's words for it, and whether
// this server can do it.
type Option struct {
	Value     any    `json:"value"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Why       string `json:"why,omitempty"`
}

// When is one dependency between settings: show the field when another has, or does not have, a
// value. Exactly one of equals and notEquals is written; its null is the unset value the page
// compares against, so the key must be present even then.
type When struct {
	Field string
	Op    string
	Value any
}

// The two comparisons a When can make.
const (
	whenEquals    = "equals"
	whenNotEquals = "notEquals"
)

// MarshalJSON writes the comparison as the one key the page reads, keeping a null value visible.
func (w When) MarshalJSON() ([]byte, error) {
	if w.Op == whenNotEquals {
		return json.Marshal(struct {
			Field     string `json:"field"`
			NotEquals any    `json:"notEquals"`
		}{w.Field, w.Value})
	}
	return json.Marshal(struct {
		Field  string `json:"field"`
		Equals any    `json:"equals"`
	}{w.Field, w.Value})
}

// The tag words a settings field can carry. set/label/choices/default/min/max are the settings
// words; the rest are the form's.
const (
	tagHelp         = "help"
	tagGroup        = "group"
	tagWidget       = "widget"
	tagUnit         = "unit"
	tagShowIf       = "showIf"
	tagUnset        = "unset"
	tagSliderLabels = "sliderLabels"
	tagNeeds        = "needs"
	tagNotYet       = "notYet"
)

// Form builds one step's card from its settings struct's tags. Env says what this server's
// workers can do, which greys a choice (a graphics card no worker has). A struct the builder
// cannot read completely is an error, never a field page 07 would draw wrongly.
func BuildForm(def Definition, env Env) (Form, error) {
	v := reflect.ValueOf(def.Settings)
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return Form{}, fmt.Errorf("flow: %s has no settings struct", def.Type)
	}
	typ := v.Type()

	fields := make([]fieldInfo, 0, typ.NumField())
	byName := make(map[string]fieldInfo, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		sf := typ.Field(i)
		name := sf.Tag.Get("set")
		if name == "" {
			continue
		}
		if _, dup := byName[name]; dup {
			return Form{}, fmt.Errorf("flow: %s has two fields set as %q", def.Type, name)
		}
		info := fieldInfo{sf: sf, name: name, order: len(fields) + 1, field: v.Field(i)}
		fields = append(fields, info)
		byName[name] = info
	}

	groups, err := cardGroups(def, fields)
	if err != nil {
		return Form{}, err
	}

	schema := Schema{Type: "object", AdditionalProperties: false, Properties: make(map[string]Property, len(fields))}
	for _, info := range fields {
		p, err := propertyOf(def, info, env)
		if err != nil {
			return Form{}, err
		}
		schema.Properties[info.name] = p
	}
	for _, info := range fields {
		p := schema.Properties[info.name]
		if p.Layout.ShowIf == nil {
			continue
		}
		target, ok := byName[p.Layout.ShowIf.Field]
		if !ok {
			return Form{}, fmt.Errorf("flow: %s.%s shows itself when %q, which is not a setting",
				def.Type, info.name, p.Layout.ShowIf.Field)
		}
		// The value is written as the target's own kind, so the page compares like with like.
		if text, ok := p.Layout.ShowIf.Value.(string); ok {
			p.Layout.ShowIf.Value = typedValue(target, text)
		}
		schema.Properties[info.name] = p
	}
	return Form{Groups: groups, Schema: schema}, nil
}

// fieldInfo is one settings field and where it sits in the card.
type fieldInfo struct {
	sf    reflect.StructField
	name  string
	order int
	field reflect.Value
}

// cardGroups is the card's sections: basic first when it has fields, then the step's own in its
// Definition's order. A field naming a group the step did not declare is an error.
func cardGroups(def Definition, fields []fieldInfo) ([]Group, error) {
	used := make(map[string]bool, len(fields))
	for _, f := range fields {
		used[f.sf.Tag.Get(tagGroup)] = true
	}
	groups := make([]Group, 0, len(def.Sections)+1)
	if used[""] || used["basic"] {
		groups = append(groups, Group{ID: "basic"})
	}
	declared := make(map[string]bool, len(def.Sections))
	for _, s := range def.Sections {
		if s.ID == "" || s.ID == "basic" {
			return nil, fmt.Errorf("flow: %s declares a section named %q", def.Type, s.ID)
		}
		declared[s.ID] = true
		if used[s.ID] {
			groups = append(groups, Group{ID: s.ID, Label: s.Label, Help: s.Help})
		}
	}
	for group := range used {
		if group == "" || group == "basic" {
			continue
		}
		if !declared[group] {
			return nil, fmt.Errorf("flow: %s has a field in section %q, which its Definition does not declare", def.Type, group)
		}
	}
	return groups, nil
}

// propertyOf reads one field's tags into its schema and layout.
func propertyOf(def Definition, info fieldInfo, env Env) (Property, error) {
	sf := info.sf
	label := sf.Tag.Get("label")
	if label == "" {
		return Property{}, fmt.Errorf("flow: %s.%s has no label tag", def.Type, info.name)
	}

	kind := sf.Type
	nullable := kind.Kind() == reflect.Pointer
	if nullable {
		kind = kind.Elem()
	}

	p := Property{Title: label, Description: sf.Tag.Get(tagHelp)}
	group := sf.Tag.Get(tagGroup)
	if group == "" {
		group = "basic"
	}
	p.Layout = Layout{Group: group, Order: info.order, Unit: sf.Tag.Get(tagUnit)}
	if labels := sf.Tag.Get(tagSliderLabels); labels != "" {
		p.Layout.SliderLabels = strings.Split(labels, ",")
	}

	switch kind.Kind() {
	case reflect.Bool:
		p.Type = "boolean"
		p.Default = info.field.Bool()
		if tagDefault, err := boolTag(sf); err != nil {
			return Property{}, fmt.Errorf("flow: %s.%s: %w", def.Type, info.name, err)
		} else if tagDefault != nil {
			p.Default = *tagDefault
		}
	case reflect.String:
		p.Type = "string"
		if nullable {
			p.Type = []string{"null", "string"}
		}
		p.Default = optionalString(info.field)
		if d, ok := sf.Tag.Lookup("default"); ok {
			p.Default = d
		}
	case reflect.Int:
		p.Type = "integer"
		if nullable {
			p.Type = []string{"null", "integer"}
		}
		p.Default = optionalInt(info.field)
		if d, ok := sf.Tag.Lookup("default"); ok {
			n, err := strconv.ParseInt(d, 10, 64)
			if err != nil {
				return Property{}, fmt.Errorf("flow: %s.%s has a default that is not a whole number: %q", def.Type, info.name, d)
			}
			p.Default = n
		}
	case reflect.Float64:
		p.Type = "number"
		if nullable {
			p.Type = []string{"null", "number"}
		}
		p.Default = optionalFloat(info.field)
		if d, ok := sf.Tag.Lookup("default"); ok {
			n, err := strconv.ParseFloat(d, 64)
			if err != nil {
				return Property{}, fmt.Errorf("flow: %s.%s has a default that is not a number: %q", def.Type, info.name, d)
			}
			p.Default = n
		}
	case reflect.Slice:
		if kind.Elem().Kind() != reflect.String {
			return Property{}, fmt.Errorf("flow: %s.%s is a list this program cannot draw", def.Type, info.name)
		}
		p.Type = "array"
		p.Items = &Items{Type: "string"}
		list := info.field
		if list.IsNil() {
			p.Default = []string{}
		} else {
			p.Default = list.Interface()
		}
	default:
		return Property{}, fmt.Errorf("flow: %s.%s has a type this program cannot draw", def.Type, info.name)
	}

	if text, ok := sf.Tag.Lookup("min"); ok {
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return Property{}, fmt.Errorf("flow: %s.%s has a min that is not a number: %q", def.Type, info.name, text)
		}
		p.Minimum = &n
	}
	if text, ok := sf.Tag.Lookup("max"); ok {
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return Property{}, fmt.Errorf("flow: %s.%s has a max that is not a number: %q", def.Type, info.name, text)
		}
		p.Maximum = &n
	}

	available, why := envAvailability(sf, env)

	choicesTag := sf.Tag.Get("choices")
	if choicesTag != "" {
		if kind.Kind() != reflect.String && kind.Kind() != reflect.Int && kind.Kind() != reflect.Float64 {
			return Property{}, fmt.Errorf("flow: %s.%s has choices but is not a choice field", def.Type, info.name)
		}
		unsetLabel := sf.Tag.Get(tagUnset)
		if nullable && unsetLabel == "" && p.Default == nil {
			return Property{}, fmt.Errorf("flow: %s.%s starts unset but the page has no words for that; add an unset tag", def.Type, info.name)
		}
		if unsetLabel != "" {
			if !nullable {
				return Property{}, fmt.Errorf("flow: %s.%s has an unset label but cannot be unset", def.Type, info.name)
			}
			p.Enum = append(p.Enum, nil)
			p.Layout.Choices = append(p.Layout.Choices, Option{Value: nil, Label: unsetLabel, Available: true})
		}
		unbuilt, err := parseNotYet(sf.Tag.Get(tagNotYet))
		if err != nil {
			return Property{}, fmt.Errorf("flow: %s.%s: %w", def.Type, info.name, err)
		}
		byValue := make(map[string]string, len(unbuilt))
		for _, u := range unbuilt {
			byValue[u.Value] = u.Why
		}
		greyed := make(map[string]bool, len(unbuilt))
		for _, c := range parseChoices(choicesTag) {
			value, err := choiceValue(kind, c.Value)
			if err != nil {
				return Property{}, fmt.Errorf("flow: %s.%s: %w", def.Type, info.name, err)
			}
			p.Enum = append(p.Enum, value)
			option := Option{Value: value, Label: c.Label, Available: true}
			if noWhy, no := byValue[c.Value]; no {
				option.Available, option.Why = false, noWhy
				greyed[c.Value] = true
			} else if !availableNow(available) {
				option.Available, option.Why = false, why
			}
			p.Layout.Choices = append(p.Layout.Choices, option)
		}
		for _, u := range unbuilt {
			if !greyed[u.Value] {
				return Property{}, fmt.Errorf("flow: %s.%s greys %q, which is not one of its choices", def.Type, info.name, u.Value)
			}
		}
	} else if sf.Tag.Get(tagNotYet) != "" {
		return Property{}, fmt.Errorf("flow: %s.%s has a notYet tag but no choices to grey", def.Type, info.name)
	}

	if widget := sf.Tag.Get(tagWidget); widget != "" {
		p.Layout.Widget = widget
	} else {
		switch kind.Kind() {
		case reflect.Bool:
			p.Layout.Widget = "checkbox"
		case reflect.Slice:
			p.Layout.Widget = "languages"
		case reflect.String:
			if choicesTag != "" {
				p.Layout.Widget = "segmented"
			} else {
				p.Layout.Widget = "text"
			}
		default:
			if choicesTag != "" {
				p.Layout.Widget = "segmented"
			} else {
				p.Layout.Widget = "number"
			}
		}
	}

	if showIf := sf.Tag.Get(tagShowIf); showIf != "" {
		when, err := parseWhen(showIf)
		if err != nil {
			return Property{}, fmt.Errorf("flow: %s.%s: %w", def.Type, info.name, err)
		}
		p.Layout.ShowIf = when
	}
	return p, nil
}

// notYetChoice is one choice a notYet tag greys: the choice's value and the sentence the page shows.
type notYetChoice struct{ Value, Why string }

// parseNotYet reads a notYet: tag, "value=why" pairs separated by commas: a choice this build
// cannot offer yet, so the page greys it with the sentence rather than letting a person pick it.
func parseNotYet(tag string) ([]notYetChoice, error) {
	if tag == "" {
		return nil, nil
	}
	var out []notYetChoice
	for _, item := range strings.Split(tag, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		value, why, ok := strings.Cut(item, "=")
		value, why = strings.TrimSpace(value), strings.TrimSpace(why)
		if !ok || value == "" || why == "" {
			return nil, fmt.Errorf("notYet wants value=why, got %q", item)
		}
		out = append(out, notYetChoice{Value: value, Why: why})
	}
	return out, nil
}

// envAvailability answers whether this server can use a choice field. A field tagged needs:"card"
// is about the graphics card: without one, every choice is greyed with the sentence saying the
// processor is doing the work. nil means no tag, so the choices are available.
func envAvailability(sf reflect.StructField, env Env) (*bool, string) {
	if sf.Tag.Get(tagNeeds) != "card" {
		return nil, ""
	}
	if env.CardFamily != "" {
		yes := true
		return &yes, ""
	}
	no := false
	return &no, "No worker has a graphics card; processor only for now."
}

func availableNow(available *bool) bool {
	return available == nil || *available
}

// choiceValue reads one choice tag value as the field's own kind.
func choiceValue(kind reflect.Type, text string) (any, error) {
	switch kind.Kind() {
	case reflect.String:
		return text, nil
	case reflect.Int:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("choice %q is not a whole number", text)
		}
		return n, nil
	case reflect.Float64:
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("choice %q is not a number", text)
		}
		return n, nil
	}
	return nil, fmt.Errorf("choices on a %s field", kind.Kind())
}

// parseWhen reads showIf:"field=value" or showIf:"field!=value". A value of null means unset.
func parseWhen(text string) (*When, error) {
	if field, value, ok := strings.Cut(text, "!="); ok {
		return &When{Field: field, Op: whenNotEquals, Value: whenValue(value)}, nil
	}
	if field, value, ok := strings.Cut(text, "="); ok {
		return &When{Field: field, Op: whenEquals, Value: whenValue(value)}, nil
	}
	return nil, fmt.Errorf("showIf %q is neither field=value nor field!=value", text)
}

// whenValue keeps "null" as the JSON null and everything else as text until the target's kind
// says otherwise.
func whenValue(text string) any {
	if text == "null" {
		return nil
	}
	return text
}

// typedValue reads a showIf value as the target field's own kind, so a number field compares
// numbers.
func typedValue(target fieldInfo, text string) any {
	kind := target.sf.Type
	if kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	switch kind.Kind() {
	case reflect.Int:
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n
		}
	case reflect.Float64:
		if n, err := strconv.ParseFloat(text, 64); err == nil {
			return n
		}
	case reflect.Bool:
		if b, err := strconv.ParseBool(text); err == nil {
			return b
		}
	}
	return text
}

func optionalString(field reflect.Value) any {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return nil
		}
		return field.Elem().String()
	}
	return field.String()
}

func optionalInt(field reflect.Value) any {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return nil
		}
		return field.Elem().Int()
	}
	return field.Int()
}

func optionalFloat(field reflect.Value) any {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return nil
		}
		return field.Elem().Float()
	}
	return field.Float()
}

// boolTag is a bool field's default:, or nil when the tag is absent.
func boolTag(sf reflect.StructField) (*bool, error) {
	text, ok := sf.Tag.Lookup("default")
	if !ok {
		return nil, nil
	}
	b, err := strconv.ParseBool(text)
	if err != nil {
		return nil, fmt.Errorf("has a default that is not yes or no: %q", text)
	}
	return &b, nil
}
