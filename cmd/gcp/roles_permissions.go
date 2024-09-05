package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

type AnalysisResult struct {
	PredefinedRoles   []string
	CustomRoles       []string
	UniquePermissions []string
}

func findUniquePermissions(
	predefinedPermissions, customPermissions mapset.Set[string],
) []string {
	uniquePermissions := customPermissions.Difference(predefinedPermissions)
	return uniquePermissions.ToSlice()
}

func getPermissionsForCustomRoles(
	iamService *iam.Service,
	projectID string,
	roles []string,
) (mapset.Set[string], error) {
	permissions := mapset.NewSet[string]()

	for _, role := range roles {
		roleInfo, err := iamService.
			Projects.
			Roles.
			Get(
				fmt.Sprintf(
					"projects/%s/roles/%s",
					projectID,
					strings.TrimPrefix(role, "projects/"+projectID+"/roles/"),
				),
			).
			Do()
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get custom role info for %s: %v",
				role,
				err,
			)
		}

		for _, permission := range roleInfo.IncludedPermissions {
			permissions.Add(permission)
		}
	}

	return permissions, nil
}

func getPermissionsForRoles(
	iamService *iam.Service,
	roles []string,
) (mapset.Set[string], error) {
	permissions := mapset.NewSet[string]()

	for _, role := range roles {
		roleInfo, err := iamService.Roles.Get(role).Do()
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get role info for %s: %v",
				role,
				err,
			)
		}

		for _, permission := range roleInfo.IncludedPermissions {
			permissions.Add(permission)
		}
	}

	return permissions, nil
}

func extractRoles(
	policy *cloudresourcemanager.Policy,
	serviceAccount string,
) ([]string, []string) {
	var predefinedRoles, customRoles []string

	for _, binding := range policy.Bindings {
		for _, member := range binding.Members {
			if member == "serviceAccount:"+serviceAccount {
				if strings.HasPrefix(binding.Role, "roles/") {
					predefinedRoles = append(predefinedRoles, binding.Role)
				} else {
					customRoles = append(customRoles, binding.Role)
				}
			}
		}
	}

	return predefinedRoles, customRoles
}

func analyzeRoles(cltx *cli.Context) error {
	projectID := cltx.String("project-id")
	serviceAccount := cltx.String("service-account")
	outputFile := cltx.String("output")
	credentialsFile := cltx.String("credentials")

	ctx := context.Background()

	iamService, rmService, err := initializeServices(ctx, credentialsFile)
	if err != nil {
		return err
	}

	result, err := performAnalysis(
		iamService,
		rmService,
		projectID,
		serviceAccount,
	)
	if err != nil {
		return err
	}

	if err := writeResultsToFile(outputFile, result); err != nil {
		return err
	}

	slog.Info("Analysis complete", "output_file", outputFile)

	return nil
}

func writeResultsToFile(
	outputFile string,
	result AnalysisResult,
) error {
	file, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %v", err)
	}
	defer file.Close()

	// Write unique permissions to file
	for _, permission := range result.UniquePermissions {
		_, err := fmt.Fprintln(file, permission)
		if err != nil {
			return fmt.Errorf("failed to write permission to file: %v", err)
		}
	}

	return nil
}

func performAnalysis(
	iamService *iam.Service,
	rmService *cloudresourcemanager.Service,
	projectID, serviceAccount string,
) (AnalysisResult, error) {
	policy, err := rmService.Projects.GetIamPolicy(projectID, &cloudresourcemanager.GetIamPolicyRequest{}).
		Do()
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("failed to get IAM policy: %v", err)
	}

	predefinedRoles, customRoles := extractRoles(policy, serviceAccount)

	predefinedPermissions, err := getPermissionsForRoles(
		iamService,
		predefinedRoles,
	)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf(
			"failed to get permissions for predefined roles: %v",
			err,
		)
	}

	customPermissions, err := getPermissionsForCustomRoles(
		iamService,
		projectID,
		customRoles,
	)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf(
			"failed to get permissions for custom roles: %v",
			err,
		)
	}

	uniquePermissions := findUniquePermissions(
		predefinedPermissions,
		customPermissions,
	)

	return AnalysisResult{
		PredefinedRoles:   predefinedRoles,
		CustomRoles:       customRoles,
		UniquePermissions: uniquePermissions,
	}, nil
}

func initializeServices(
	ctx context.Context,
	credentialsFile string,
) (*iam.Service, *cloudresourcemanager.Service, error) {
	opts := option.WithCredentialsFile(credentialsFile)

	iamService, err := iam.NewService(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to create IAM service client: %v",
			err,
		)
	}

	rmService, err := cloudresourcemanager.NewService(ctx, opts)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to create Resource Manager service client: %v",
			err,
		)
	}

	return iamService, rmService, nil
}
