package cli

// scheduleVerbs are the read-only schedule verbs: `schedule list` and nothing
// else.
//
// Create, update and delete are writes, and a verb for them would have to be
// guarded or would quietly let an agent change when a workflow runs — which is
// exactly the decision design §4.7 makes the user's. `kilasflow api
// create-schedule` reaches the write today, and phase 2 can add the guarded
// verbs when a scoped token exists to refuse them.
func scheduleVerbs() []Verb {
	return []Verb{
		{
			Path:      "schedule list",
			Operation: "list-schedules",
			Summary:   "list cron schedules (`--limit`, `--cursor`)",
			Flags:     registerPageFlags,
			Run:       runScheduleList,
			Human:     humanResourceList("id", "workflowId", "cron", "active", "nextRunAt"),
		},
	}
}

// runScheduleList reads one page of schedules.
func runScheduleList(ctx *Context, args []string) error {
	if err := refusePositional(args, "schedule list"); err != nil {
		return err
	}

	list, err := ctx.listPage("/schedules", pageQuery(ctx))
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}
