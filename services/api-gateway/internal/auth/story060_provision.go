package auth

import (
	"context"
	"errors"
)

var ErrStory060ProvisioningInput = errors.New("invalid STORY-060 provisioning input")

type Story060ProvisionStore interface {
	ProvisionStory060User(ctx context.Context, username, displayName, roleCode, passwordHash string) error
}

func ProvisionStory060Users(ctx context.Context, store Story060ProvisionStore, schoolAdminPassword, workerPassword string) error {
	if !PasswordMeetsMinimum(schoolAdminPassword, AdministratorPasswordMinimum) || !PasswordMeetsMinimum(workerPassword, 15) {
		return ErrStory060ProvisioningInput
	}
	users := []struct{ username, displayName, roleCode, password string }{
		{"story060_school_admin", "STORY-060 E2E School Admin", "school_admin", schoolAdminPassword},
		{"story060_subjective_worker", "STORY-060 E2E Subjective Worker", "subjective_grading_worker", workerPassword},
	}
	for _, user := range users {
		hash, err := HashPassword(user.password)
		if err != nil {
			return err
		}
		if err := store.ProvisionStory060User(ctx, user.username, user.displayName, user.roleCode, hash); err != nil {
			return err
		}
	}
	return nil
}
