package order

import (
	"reflect"

	"github.com/google/uuid"
)

func extractUUIDReflect(obj interface{}, field string) uuid.UUID {
	if obj == nil {
		return uuid.Nil
	}
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return uuid.Nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return uuid.Nil
	}
	f := v.FieldByName(field)
	if !f.IsValid() {
		return uuid.Nil
	}
	if f.Type() == reflect.TypeOf(uuid.UUID{}) {
		if id, ok := f.Interface().(uuid.UUID); ok {
			return id
		}
	}
	if f.Kind() == reflect.Pointer && !f.IsNil() {
		if id, ok := f.Interface().(*uuid.UUID); ok && id != nil {
			return *id
		}
	}
	return uuid.Nil
}

func extractStringReflect(obj interface{}, field string) string {
	if obj == nil {
		return ""
	}
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName(field)
	if !f.IsValid() {
		return ""
	}
	if f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}

func extractStringPtrReflect(obj interface{}, field string) *string {
	if obj == nil {
		return nil
	}
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	f := v.FieldByName(field)
	if !f.IsValid() {
		return nil
	}
	if f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.String {
		if f.IsNil() {
			return nil
		}
		if s, ok := f.Interface().(*string); ok {
			return s
		}
	}
	if f.Kind() == reflect.String {
		s := f.String()
		return &s
	}
	return nil
}
