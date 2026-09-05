# Authored fixtures

KilasFlow's own n8n workflow documents. Unlike the rest of the corpus these are
committed, because this project wrote them and owns them.

They exist as **controls**. Almost every upstream fixture fails today, and a
scoreboard where everything fails cannot distinguish "importing real n8n
workflows is genuinely hard" from "the measuring instrument is broken". A
control that must pass every tier makes that difference visible in the report.

A fixture here failing is a bug in KilasFlow. A fixture from `waha-templates` or
`nodes-base` failing is a measurement, and is what the baseline is for.
