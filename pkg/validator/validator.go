package validator

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()

	// Use JSON field name in error messages instead of struct field name
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
}

// Validate validates a struct and returns a slice of ValidationError.
// Returns nil if there are no errors.
func Validate(s any) []response.ValidationError {
	err := validate.Struct(s)
	if err == nil {
		return nil
	}

	var errs []response.ValidationError
	for _, fe := range err.(validator.ValidationErrors) {
		errs = append(errs, response.ValidationError{
			Field:   fe.Field(),
			Message: humanize(fe),
		})
	}
	return errs
}

// humanize converts a validator.FieldError into a human-readable message.
func humanize(fe validator.FieldError) string {
	field := fe.Field()
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", field)
	case "email":
		return fmt.Sprintf("%s must be a valid email address", field)
	case "min":
		return fmt.Sprintf("%s must be at least %s characters", field, fe.Param())
	case "max":
		return fmt.Sprintf("%s must be at most %s characters", field, fe.Param())
	case "len":
		return fmt.Sprintf("%s must be exactly %s characters", field, fe.Param())
	case "numeric":
		return fmt.Sprintf("%s must contain only numbers", field)
	case "alphanum":
		return fmt.Sprintf("%s must contain only letters and numbers", field)
	case "url":
		return fmt.Sprintf("%s must be a valid URL", field)
	case "uuid":
		return fmt.Sprintf("%s must be a valid UUID", field)
	case "gt":
		return fmt.Sprintf("%s must be greater than %s", field, fe.Param())
	case "gte":
		return fmt.Sprintf("%s must be at least %s", field, fe.Param())
	case "lt":
		return fmt.Sprintf("%s must be less than %s", field, fe.Param())
	case "lte":
		return fmt.Sprintf("%s must be at most %s", field, fe.Param())
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, fe.Param())
	default:
		return fmt.Sprintf("%s is invalid (%s)", field, fe.Tag())
	}
}
