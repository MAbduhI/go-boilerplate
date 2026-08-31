// Package validation supplements Gin's binding tags with cross-field rules.
//
// Deliberately thin: per-field rules belong in `binding:"..."` tags on the
// request struct. This package exists only for the rules a tag cannot express
// — comparisons between two fields, conditional requirements, custom formats —
// and for turning validator errors into per-field API detail.
//
// It hooks Gin's own validator engine rather than constructing a second one, so
// registered rules are usable directly from binding tags and no separate
// validation pass is needed.
package validation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
)

var (
	once   sync.Once
	engine *validator.Validate
)

// init registers the JSON field-name resolver eagerly.
//
// Binding goes through Gin, which never calls Engine(), so a purely lazy
// registration would leave API error details naming Go struct fields ("Name")
// instead of the JSON keys the client actually sent ("name").
func init() { _ = Engine() }

// Engine returns Gin's underlying validator, configured to report field names
// as they appear in JSON so API error details match the request body.
func Engine() *validator.Validate {
	once.Do(func() {
		v, ok := binding.Validator.Engine().(*validator.Validate)
		if !ok {
			// Gin's default binding.Validator was replaced; fall back to our own
			// so callers still get a working engine.
			v = validator.New()
		}
		v.RegisterTagNameFunc(jsonTagName)
		engine = v
	})
	return engine
}

// jsonTagName prefers the json tag over the Go field name.
func jsonTagName(fld reflect.StructField) string {
	name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
	if name == "-" || name == "" {
		return fld.Name
	}
	return name
}

// Register adds a named rule usable as `binding:"<tag>"`.
//
//	validation.Register("slug", func(fl validator.FieldLevel) bool {
//	    return slugRe.MatchString(fl.Field().String())
//	})
func Register(tag string, fn validator.Func, callValidationEvenIfNull ...bool) error {
	return Engine().RegisterValidation(tag, fn, callValidationEvenIfNull...)
}

// RegisterStruct adds a struct-level rule, the hook for cross-field logic that
// needs to see the whole value at once.
//
//	validation.RegisterStruct(DateRange{}, func(sl validator.StructLevel) {
//	    r := sl.Current().Interface().(DateRange)
//	    if r.End.Before(r.Start) {
//	        sl.ReportError(r.End, "end", "End", "gtfield", "start")
//	    }
//	})
func RegisterStruct(target any, fn validator.StructLevelFunc) {
	Engine().RegisterStructValidation(fn, target)
}

// RegisterAlias gives a reusable name to a rule chain.
func RegisterAlias(alias, tags string) { Engine().RegisterAlias(alias, tags) }

// Struct validates a value outside the request-binding path, for example in a
// usecase invoked by a worker rather than an HTTP handler.
func Struct(v any) error { return Engine().Struct(v) }

// FieldErrors converts a validator error into a field -> message map suitable
// for an API error payload. Non-validator errors yield a single "_error" key so
// the caller always has something structured to return.
func FieldErrors(err error) map[string]any {
	if err == nil {
		return nil
	}

	var invalid *validator.InvalidValidationError
	if errors.As(err, &invalid) {
		return map[string]any{"_error": "validation target is not a struct"}
	}

	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return map[string]any{"_error": err.Error()}
	}

	out := make(map[string]any, len(verrs))
	for _, fe := range verrs {
		out[fe.Field()] = message(fe)
	}
	return out
}

// message renders a human-readable reason for one failed rule.
func message(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "url":
		return "must be a valid URL"
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "min":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "len":
		return fmt.Sprintf("must be exactly %s in length", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", strings.ReplaceAll(fe.Param(), " ", ", "))
	case "gtfield":
		return fmt.Sprintf("must be greater than %s", fe.Param())
	case "gtefield":
		return fmt.Sprintf("must be greater than or equal to %s", fe.Param())
	case "ltfield":
		return fmt.Sprintf("must be less than %s", fe.Param())
	case "ltefield":
		return fmt.Sprintf("must be less than or equal to %s", fe.Param())
	case "eqfield":
		return fmt.Sprintf("must equal %s", fe.Param())
	case "nefield":
		return fmt.Sprintf("must not equal %s", fe.Param())
	case "required_with":
		return fmt.Sprintf("is required when %s is present", fe.Param())
	case "required_without":
		return fmt.Sprintf("is required when %s is absent", fe.Param())
	default:
		if fe.Param() != "" {
			return fmt.Sprintf("failed rule %s=%s", fe.Tag(), fe.Param())
		}
		return fmt.Sprintf("failed rule %s", fe.Tag())
	}
}
