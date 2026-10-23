package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
)

func (a *App) newDoctorCmd() *cobra.Command {
	var repair bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run diagnostic checks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			var report doctor.Report
			if repair {
				report, err = c.Repair(ctx)
			} else {
				report, err = c.Doctor(ctx)
			}
			if err != nil {
				return err
			}
			return a.printDoctor(cmd, report)
		},
	}
	cmd.Flags().BoolVar(&repair, "repair", false, "re-apply the safe parts of the installation before reporting")
	return cmd
}

func (a *App) printDoctor(cmd *cobra.Command, report doctor.Report) error {
	if report.Checks == nil {
		report.Checks = []doctor.Check{}
	}
	if a.jsonOutput {
		if err := a.writeJSON(cmd, report); err != nil {
			return err
		}
	} else {
		rows := make([][]string, 0, len(report.Checks))
		for _, check := range report.Checks {
			rows = append(rows, []string{
				string(check.Status),
				check.ID,
				check.Title,
				defaultString(check.Detail, "-"),
				defaultString(check.Remediation, "-"),
			})
		}
		if err := writeTable(cmd, []string{"STATUS", "ID", "CHECK", "DETAIL", "REMEDIATION"}, rows); err != nil {
			return err
		}
		failed := 0
		for _, check := range report.Checks {
			if check.Status == doctor.StatusFail {
				failed++
			}
		}
		if report.OK {
			fmt.Fprintln(cmd.OutOrStdout(), "\nAll checks passed.")
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%d check(s) failed.\n", failed)
		}
	}
	if !report.OK {
		return &Error{
			Code:        "DOCTOR_FAILED",
			Message:     "doctor found failing checks",
			Remediation: "Review the remediation column, then run `devstack doctor --repair`.",
		}
	}
	return nil
}

func (a *App) newTrustCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Manage local certificate authority trust",
	}
	cmd.AddCommand(
		a.newTrustActionCmd("status"),
		a.newTrustActionCmd("repair"),
		a.newTrustActionCmd("remove"),
	)
	return cmd
}

func (a *App) newTrustActionCmd(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: titleAction(action) + " CA trust",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			var targets []trust.Target
			switch action {
			case "status":
				targets, err = c.TrustStatus(ctx)
			case "repair":
				targets, err = c.TrustRepair(ctx)
			case "remove":
				targets, err = c.TrustRemove(ctx)
			}
			if err != nil {
				return err
			}
			if targets == nil {
				targets = []trust.Target{}
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, targets)
			}
			rows := make([][]string, 0, len(targets))
			failures := 0
			for _, target := range targets {
				if !target.OK {
					failures++
				}
				rows = append(rows, []string{target.Name, fmt.Sprintf("%t", target.OK), defaultString(target.Detail, "-")})
			}
			if err := writeTable(cmd, []string{"TARGET", "OK", "DETAIL"}, rows); err != nil {
				return err
			}
			if failures != 0 {
				return &Error{
					Code:        "TRUST_FAILED",
					Message:     fmt.Sprintf("%d trust target(s) need attention", failures),
					Remediation: "Run `devstack trust repair`, then rerun `devstack trust status`.",
				}
			}
			return nil
		},
	}
}
