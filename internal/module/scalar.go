// Copyright 2021-2023 Zenauth Ltd.
// SPDX-License-Identifier: Apache-2.0

package module

import (
	"io"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"

	pgs "github.com/lyft/protoc-gen-star/v2"

	"github.com/cerbos/protoc-gen-jsonschema/gen/pb/buf/validate"
	"github.com/cerbos/protoc-gen-jsonschema/internal/jsonschema"
)

func (m *Module) schemaForScalar(scalar pgs.ProtoType, constraints *validate.FieldConstraints) (jsonschema.Schema, bool) {
	m.Debug("schemaForScalar")
	if scalar.IsNumeric() {
		return m.schemaForNumericScalar(scalar, constraints)
	}

	ignoreEmpty := false
	if constraints != nil {
		ignoreEmpty = constraints.IgnoreEmpty
	}

	switch scalar {
	case pgs.BoolT:
		return m.schemaForBool(constraints.GetBool())

	case pgs.BytesT:
		return m.schemaForBytes(constraints.GetBytes(), ignoreEmpty)

	case pgs.StringT:
		return m.schemaForString(constraints.GetString_(), ignoreEmpty)

	default:
		m.Failf("unexpected scalar type %q", scalar)
		return nil, false
	}
}

func (m *Module) schemaForBool(rules *validate.BoolRules) (jsonschema.Schema, bool) {
	m.Debug("schemaForBool")
	required := false
	schema := jsonschema.NewBooleanSchema()

	if rules != nil {
		if rules.Const != nil {
			schema.Const = jsonschema.Boolean(rules.GetConst())
			required = true
		}
	}

	return schema, required
}

func (m *Module) schemaForBytes(rules *validate.BytesRules, ignoreEmpty bool) (jsonschema.Schema, bool) {
	m.Debug("schemaForBytes")
	required := false

	standard := jsonschema.NewStringSchema()
	standard.Title = "Standard base64 encoding"
	standard.Pattern = `^[\r\nA-Za-z0-9+/]*$`

	urlSafe := jsonschema.NewStringSchema()
	urlSafe.Title = "URL-safe base64 encoding"
	urlSafe.Pattern = `^[\r\nA-Za-z0-9_-]*$`

	schema := jsonschema.NewStringSchema()
	schema.OneOf = []jsonschema.NonTrivialSchema{standard, urlSafe}

	if rules != nil {
		required = !ignoreEmpty &&
			(len(rules.Const) > 0 ||
				len(rules.Contains) > 0 ||
				len(rules.In) > 0 ||
				rules.MinLen != nil ||
				rules.Pattern != nil ||
				len(rules.Prefix) > 0 ||
				len(rules.Suffix) > 0 ||
				rules.WellKnown != nil)
	}

	return schema, required
}

func (m *Module) schemaForString(rules *validate.StringRules, ignoreEmpty bool) (jsonschema.Schema, bool) {
	m.Debug("schemaForString")
	required := false
	schema := jsonschema.NewStringSchema()
	schemas := []jsonschema.NonTrivialSchema{schema}
	var patterns []string

	//nolint:nestif
	if rules != nil {
		if rules.Const != nil {
			schema.Const = jsonschema.String(rules.GetConst())
			required = !ignoreEmpty
		}

		if rules.Contains != nil {
			patterns = append(patterns, regexp.QuoteMeta(rules.GetContains()))
			required = !ignoreEmpty
		}

		if len(rules.In) > 0 {
			schema.Enum = rules.In
			required = !ignoreEmpty
		}

		if rules.Len != nil {
			schema.MaxLength = jsonschema.Size(rules.GetLen())
			schema.MinLength = jsonschema.Size(rules.GetLen())
			required = !ignoreEmpty
		}

		if rules.LenBytes != nil || rules.MinBytes != nil {
			required = !ignoreEmpty
		}

		if rules.MaxLen != nil {
			schema.MaxLength = jsonschema.Size(rules.GetMaxLen())
		}

		if rules.MinLen != nil {
			schema.MinLength = jsonschema.Size(rules.GetMinLen())
			required = !ignoreEmpty
		}

		if rules.NotContains != nil {
			contains := jsonschema.NewStringSchema()
			contains.Pattern = regexp.QuoteMeta(rules.GetNotContains())
			schemas = append(schemas, jsonschema.Not(contains))
		}

		if len(rules.NotIn) > 0 {
			in := jsonschema.NewStringSchema()
			in.Enum = rules.NotIn
			schemas = append(schemas, jsonschema.Not(in))
		}

		if rules.Pattern != nil {
			patterns = append(patterns, m.makeRegexpCompatibleWithECMAScript(rules.GetPattern()))
			if !m.matchesEmptyString(rules.GetPattern()) {
				required = !ignoreEmpty
			}
		}

		if rules.Prefix != nil {
			patterns = append(patterns, "^"+regexp.QuoteMeta(rules.GetPrefix()))
			required = !ignoreEmpty
		}

		if rules.Suffix != nil {
			patterns = append(patterns, regexp.QuoteMeta(rules.GetSuffix())+"$")
			required = !ignoreEmpty
		}

		if rules.WellKnown != nil {
			switch rules.WellKnown.(type) {
			case *validate.StringRules_Address:
				schemas = append(schemas, m.schemaForStringFormats(jsonschema.StringFormatHostname, jsonschema.StringFormatIPv4, jsonschema.StringFormatIPv6))

			case *validate.StringRules_Email:
				schema.Format = jsonschema.StringFormatEmail

			case *validate.StringRules_Hostname:
				schema.Format = jsonschema.StringFormatHostname

			case *validate.StringRules_Ip:
				schemas = append(schemas, m.schemaForStringFormats(jsonschema.StringFormatIPv4, jsonschema.StringFormatIPv6))

			case *validate.StringRules_Ipv4:
				schema.Format = jsonschema.StringFormatIPv4

			case *validate.StringRules_Ipv6:
				schema.Format = jsonschema.StringFormatIPv6

			case *validate.StringRules_Uri:
				schema.Format = jsonschema.StringFormatURI

			case *validate.StringRules_UriRef:
				schema.Format = jsonschema.StringFormatURIReference
			}

			required = !ignoreEmpty
		}
	}

	if len(patterns) == 1 {
		schema.Pattern = patterns[0]
	} else {
		for _, pattern := range patterns {
			match := jsonschema.NewStringSchema()
			match.Pattern = pattern
			schemas = append(schemas, match)
		}
	}

	return jsonschema.AllOf(schemas...), required
}

func (m *Module) schemaForStringFormats(formats ...jsonschema.StringFormat) jsonschema.NonTrivialSchema {
	m.Debug("schemaForStringFormats")
	schemas := make([]jsonschema.NonTrivialSchema, len(formats))

	for i, format := range formats {
		schema := jsonschema.NewStringSchema()
		schema.Format = format
		schemas[i] = schema
	}

	return jsonschema.AnyOf(schemas...)
}

func (m *Module) makeRegexpCompatibleWithECMAScript(pattern string) string {
	m.Debug("makeRegexpCompatibleWithECMAScript")
	expression, err := syntax.Parse(pattern, syntax.Perl)
	m.CheckErr(err, "failed to parse regular expression")

	var builder strings.Builder
	writeECMAScriptCompatibleRegexp(&builder, expression)
	return builder.String()
}

func writeECMAScriptCompatibleRegexp(w io.StringWriter, expression *syntax.Regexp) {
	switch expression.Op {
	case syntax.OpAnyCharNotNL:
		w.WriteString(`.`) //nolint:errcheck
	case syntax.OpAnyChar:
		w.WriteString(`[\s\S]`) //nolint:errcheck
	case syntax.OpBeginLine, syntax.OpBeginText:
		w.WriteString(`^`) //nolint:errcheck
	case syntax.OpEndLine, syntax.OpEndText:
		w.WriteString(`$`) //nolint:errcheck
	case syntax.OpCapture:
		w.WriteString(`(`) //nolint:errcheck
		writeECMAScriptCompatibleRegexp(w, expression.Sub[0])
		w.WriteString(`)`) //nolint:errcheck
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		subexpression := expression.Sub[0]
		if subexpression.Op > syntax.OpCapture || (subexpression.Op == syntax.OpLiteral && len(subexpression.Rune) > 1) {
			w.WriteString(`(?:`) //nolint:errcheck
			writeECMAScriptCompatibleRegexp(w, subexpression)
			w.WriteString(`)`) //nolint:errcheck
		} else {
			writeECMAScriptCompatibleRegexp(w, subexpression)
		}

		switch expression.Op {
		case syntax.OpStar:
			w.WriteString(`*`) //nolint:errcheck

		case syntax.OpPlus:
			w.WriteString(`+`) //nolint:errcheck

		case syntax.OpQuest:
			w.WriteString(`?`) //nolint:errcheck

		case syntax.OpRepeat:
			w.WriteString(`{`)                          //nolint:errcheck
			w.WriteString(strconv.Itoa(expression.Min)) //nolint:errcheck
			if expression.Max != expression.Min {
				w.WriteString(`,`) //nolint:errcheck
				if expression.Max >= 0 {
					w.WriteString(strconv.Itoa(expression.Max)) //nolint:errcheck
				}
			}
			w.WriteString(`}`) //nolint:errcheck
		default:
		}
	case syntax.OpConcat:
		for _, subexpression := range expression.Sub {
			if subexpression.Op == syntax.OpAlternate {
				w.WriteString(`(?:`) //nolint:errcheck
				writeECMAScriptCompatibleRegexp(w, subexpression)
				w.WriteString(`)`) //nolint:errcheck
			} else {
				writeECMAScriptCompatibleRegexp(w, subexpression)
			}
		}
	case syntax.OpAlternate:
		for i, subexpression := range expression.Sub {
			if i > 0 {
				w.WriteString(`|`) //nolint:errcheck
			}
			writeECMAScriptCompatibleRegexp(w, subexpression)
		}
	default:
		w.WriteString(expression.String()) //nolint:errcheck
	}
}

func (m *Module) matchesEmptyString(pattern string) bool {
	m.Debug("matchesEmptyString")
	match, err := regexp.MatchString(pattern, "")
	m.CheckErr(err, "failed to check if pattern matches empty string")
	return match
}
