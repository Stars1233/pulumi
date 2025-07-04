// Copyright 2016-2024, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package state

import (
	"errors"
	"fmt"

	"github.com/pulumi/pulumi/pkg/v3/backend/display"
	"github.com/pulumi/pulumi/pkg/v3/cmd/pulumi/backend"
	"github.com/pulumi/pulumi/pkg/v3/resource/deploy"
	"github.com/pulumi/pulumi/pkg/v3/resource/edit"
	pkgWorkspace "github.com/pulumi/pulumi/pkg/v3/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/env"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"

	"github.com/spf13/cobra"
)

func newStateDeleteCommand(ws pkgWorkspace.Context, lm backend.LoginManager) *cobra.Command {
	var force bool // Force deletion of protected resources
	var stack string
	var yes bool
	var targetDependents bool
	var all bool

	cmd := &cobra.Command{
		Use:   "delete [resource URN]",
		Short: "Deletes a resource from a stack's state",
		Long: `Deletes a resource from a stack's state

This command deletes a resource from a stack's state, as long as it is safe to do so. The resource is specified
by its Pulumi URN. If the URN is omitted, this command will prompt for it.

Resources can't be deleted if other resources depend on it or are parented to it. Protected resources
will not be deleted unless specifically requested using the --force flag.

Make sure that URNs are single-quoted to avoid having characters unexpectedly interpreted by the shell.

To see the list of URNs in a stack, use ` + "`pulumi stack --show-urns`" + `.
`,
		Example: "pulumi state delete 'urn:pulumi:stage::demo::eks:index:Cluster$pulumi:providers:kubernetes::eks-provider'",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			sink := cmdutil.Diag()
			yes = yes || env.SkipConfirmations.Value()
			var urn resource.URN
			if all {
				if len(args) != 0 {
					return errors.New("cannot specify a resource URN when deleting all resources")
				}
			} else {
				if len(args) == 0 {
					if !cmdutil.Interactive() {
						return missingNonInteractiveArg("resource URN")
					}

					var err error
					urn, err = getURNFromState(ctx, sink, ws, backend.DefaultLoginManager, stack, nil,
						"Select the resource to delete")
					if err != nil {
						return fmt.Errorf("failed to select resource: %w", err)
					}
				} else {
					urn = resource.URN(args[0])
				}
			}
			// Show the confirmation prompt if the user didn't pass the --yes parameter to skip it.
			showPrompt := !yes

			var handleProtected func(*resource.State) error
			if force {
				handleProtected = func(res *resource.State) error {
					cmdutil.Diag().Warningf(diag.Message(res.URN,
						"deleting protected resource %s due to presence of --force"), res.URN)
					res.Protect = false
					return nil
				}
			}

			// If we're deleting everything then run a total state edit, else run on just the resource given.
			var err error
			if all {
				err = runTotalStateEdit(ctx, sink, ws, lm, stack, showPrompt,
					func(opts display.Options, snap *deploy.Snapshot) error {
						// Iterate the resources backwards (so we delete dependents first) and delete them.
						for i := len(snap.Resources) - 1; i >= 0; i-- {
							res := snap.Resources[i]
							if err := edit.DeleteResource(snap, res, handleProtected, targetDependents); err != nil {
								return err
							}
						}
						return nil
					})
			} else {
				err = runStateEdit(
					ctx, sink, ws, lm, stack, showPrompt, urn, func(snap *deploy.Snapshot, res *resource.State) error {
						return edit.DeleteResource(snap, res, handleProtected, targetDependents)
					})
			}
			if err != nil {
				switch e := err.(type) {
				case edit.ResourceHasDependenciesError:
					message := string(e.Condemned.URN) + " can't be safely deleted because the following resources depend on it:\n"
					for _, dependentResource := range e.Dependencies {
						depUrn := dependentResource.URN
						message += fmt.Sprintf(" * %-15q (%s)\n", depUrn.Name(), depUrn)
					}

					message += "\nDelete those resources first or pass --target-dependents."
					return errors.New(message)
				case edit.ResourceProtectedError:
					return fmt.Errorf(
						"%s can't be safely deleted because it is protected. "+
							"Re-run this command with --force to force deletion", string(e.Condemned.URN))
				default:
					return err
				}
			}
			if all {
				fmt.Println("Resources deleted")
			} else {
				fmt.Println("Resource deleted")
			}
			return nil
		},
	}

	cmd.PersistentFlags().StringVarP(
		&stack, "stack", "s", "",
		"The name of the stack to operate on. Defaults to the current stack")
	cmd.Flags().BoolVar(&force, "force", false, "Force deletion of protected resources")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&all, "all", false, "Delete all resources in the stack")
	cmd.Flags().BoolVar(&targetDependents, "target-dependents", false, "Delete the URN and all its dependents")
	return cmd
}
