// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package workflow is the v2 workflow model (UI-API-CONTRACT-v2.md; slices/v2-engine/proposal.md):
// one schema for the stack and the graph, the node registry, validation, and the mapping between a
// workflow and the rules.Template the engine runs today. It has no storage of its own.
package workflow

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// SchemaVersion is the Doc shape's version (research workflow-schema.md): raise it for file-level
// changes; a node type's own changes raise its TypeVersion.
const SchemaVersion = 1

// Doc is a workflow's logic: what a published revision freezes. Layout is kept apart from it.
type Doc struct {
	SchemaVersion int                `json:"schemaVersion"`
	Nodes         []Node             `json:"nodes"`
	Edges         []Edge             `json:"edges"`
	Variables     []Variable         `json:"variables"`
	Subflows      map[string]Subflow `json:"subflows,omitempty"`
}

// Subflow is reserved for a later phase.
type Subflow struct {
	Name  string `json:"name"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node is one step. Nothing refers to a node by its name.
type Node struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	TypeVersion int            `json:"typeVersion"`
	Name        string         `json:"name,omitempty"`
	Config      map[string]any `json:"config"`
	Enabled     bool           `json:"enabled"`
	OnError     *ErrorPolicy   `json:"onError,omitempty"`
}

// ErrorPolicy is what a node's unwired "error" port means.
type ErrorPolicy struct {
	Action         string `json:"action"` // skip | stop | retry | fallback
	Max            int    `json:"max,omitempty"`
	BackoffSeconds int    `json:"backoffSeconds,omitempty"`
	Then           string `json:"then,omitempty"` // retry: skip | stop
	To             string `json:"to,omitempty"`   // fallback: a node id
}

// Edge joins an output port to an input port, by node id.
type Edge struct {
	ID       string `json:"id"`
	From     string `json:"from"`
	FromPort string `json:"fromPort"`
	To       string `json:"to"`
	ToPort   string `json:"toPort"`
	Label    string `json:"label,omitempty"`
}

// Variable is a workflow variable ({{name}}); a secret's value is never sent back.
type Variable struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Value         any      `json:"value,omitempty"`
	HasValue      bool     `json:"hasValue,omitempty"`
	Options       []string `json:"options,omitempty"`
	Description   string   `json:"description,omitempty"`
	TemplateParam bool     `json:"templateParam,omitempty"`
}

// Layout is how the graph is drawn: never part of the logic.
type Layout struct {
	Nodes    map[string]NodeLayout `json:"nodes"`
	Groups   []Group               `json:"groups"`
	Comments []Comment             `json:"comments"`
	Viewport *Viewport             `json:"viewport,omitempty"`
}

type NodeLayout struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Collapsed bool    `json:"collapsed,omitempty"`
}

type Group struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	NodeIDs   []string `json:"nodeIds"`
	Collapsed bool     `json:"collapsed"`
}

type Comment struct {
	ID   string  `json:"id"`
	Text string  `json:"text"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}

// Issue is one validation finding; errors block Publish.
type Issue struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"` // error | warning
	Message    string `json:"message"`
	NodeID     string `json:"nodeId,omitempty"`
	EdgeID     string `json:"edgeId,omitempty"`
	Port       string `json:"port,omitempty"`
	ConfigPath string `json:"configPath,omitempty"`
	Field      string `json:"field,omitempty"` // the setting a config problem is about (its key)
}

// LogicHash identifies a Doc's logic: equal Docs (whatever their layout) hash alike.
func LogicHash(d Doc) string {
	b, _ := json.Marshal(d) // map keys are sorted by encoding/json, so this is stable
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// NewID is a random UUID (version 4) for a node or edge.
func NewID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
