package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const (
	maxTenantCodeBytes   = 128
	maxUsernameBytes     = 256
	maxPhoneBytes        = 32
	maxEmployeeNoBytes   = 128
	maxDisplayNameBytes  = 256
	maxRoleCodeBytes     = 128
	maxPasswordBytes     = 1024
	maxAuthJSONBodyBytes = 4 * 1024
)

// 认证请求限制总字节数，并且只接受一个 JSON 值，避免合法对象后夹带额外内容被忽略。
func decodeAuthJSON(w http.ResponseWriter, r *http.Request, target any, disallowUnknownFields bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func authRequestBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func loginFieldsWithinLimits(tenantCode string, username string, password string) bool {
	return len(tenantCode) <= maxTenantCodeBytes &&
		len(username) <= maxUsernameBytes &&
		len(password) <= maxPasswordBytes
}

func managedUserFieldsWithinLimits(input CreateManagedUserInput) bool {
	if len(input.ClassIDs) > 200 || len(input.SchoolID) > 128 {
		return false
	}
	for _, classID := range input.ClassIDs {
		if len(classID) > 128 {
			return false
		}
	}
	return len(input.Username) <= maxUsernameBytes &&
		len(input.Phone) <= maxPhoneBytes &&
		len(input.EmployeeNo) <= maxEmployeeNoBytes &&
		len(input.DisplayName) <= maxDisplayNameBytes &&
		len(input.RoleCode) <= maxRoleCodeBytes &&
		len(input.Password) <= maxPasswordBytes
}

func bootstrapFieldsWithinLimits(input BootstrapAdminInput) bool {
	return len(input.TenantCode) <= maxTenantCodeBytes &&
		len(input.Username) <= maxUsernameBytes &&
		len(input.DisplayName) <= maxDisplayNameBytes &&
		len(input.RoleCode) <= maxRoleCodeBytes &&
		len(input.Password) <= maxPasswordBytes
}
