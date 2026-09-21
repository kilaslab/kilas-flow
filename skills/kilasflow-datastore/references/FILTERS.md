# Filter grammar

A filter is what says which rows a read, update, delete, upsert or increment
acts on. It is one grammar everywhere: the management API's read, the node's
conditions panel and the read-only agent tool all compile to it
(internal/datastore/filter.go).

## The two shapes

The **service envelope** is what the API takes and what the node's panel
compiles to:

```json
{
  "type": "and",
  "filters": [
    { "columnName": "status", "condition": "eq", "value": "active" },
    { "columnName": "score", "condition": "gte", "value": 10 }
  ]
}
```

`type` is `and` or `or`; `and`/`all`/`all condition(s)`,
`or`/`any`/`any condition(s)` and the empty string are all accepted spellings,
anything else is a 422 naming the two (`buildFilterClause`).

The **node panel** stores one row per condition, verbatim from the editor:
`filters.conditions[i].keyName` (the column), `.condition` (default `eq`) and
`.keyValue` (the value), with `match` on the node naming Any Condition (`any`)
or All Conditions (`all`). `NodeConditionsToFilter` renames `keyName` to
`columnName` and `keyValue` to `value`, so the panel and the API are one
grammar (`internal/datastore/filter.go`, nodes/datastore.go).

## Operators

Ten, and an unrecognised one is an error naming the set — never a dropped
predicate that leaves a WHERE clause which still parses:

| `condition` | Means | Fragment |
| --- | --- | --- |
| `eq` | equals | `= ?` |
| `neq` | not equals | `<> ?` |
| `like` | contains, case-sensitive | `GLOB ?` on SQLite, `LIKE ?` on PostgreSQL |
| `ilike` | contains, case-insensitive | `LIKE ?` on SQLite, `ILIKE ?` on PostgreSQL |
| `gt` / `gte` | greater than / or equal | `> ?` / `>= ?` |
| `lt` / `lte` | less than / or equal | `< ?` / `<= ?` |
| `isEmpty` | null, or the empty string on text | no placeholder |
| `isNotEmpty` | the negation of `isEmpty` | no placeholder |

The LIKE pair is the one place a dialect shows: PostgreSQL's `LIKE` is
case-sensitive while SQLite's is insensitive for ASCII and has no `ILIKE`, so
`like` is the case-sensitive match on both — `%` becomes `*`, `_` becomes `?`
and every GLOB metacharacter in the literal is bracketed — and `ilike` is the
insensitive one on both. `LIKE ESCAPE` is not supported: a backslash is literal
text (`conditionFragment`, `likePatternToGlob`).

`isEmpty` is null plus, for a `string` column only, the empty string: a string
holding `""` reads empty in the grid, a number holding `0` does not.

## Values

Values never reach SQL as text: every one is bound as a `?` placeholder, so the
composed statement contains no byte of a supplied value (`buildFilterClause`).

- **A null value** is not a missing value. `eq` with a null value compiles to
  `IS NULL` and `neq` to `IS NOT NULL`; any other operator with a null value is
  a 422 naming the column.
- **A value is coerced to its column's type** the way a write coerces it, with
  two filter rules: a LIKE pattern must be a string, and the `id` system column
  binds as an integer rather than a float (`coerceFilterValue`).
- **Over the query string** the value arrives untyped, so one that parses as
  JSON takes the parsed type — `3` is a number, `true` a boolean — and anything
  else stays the string you wrote, which is what a LIKE pattern always is
  (`queryValue` in internal/api/handlers/datastores.go).
- **Inside a node**, `keyValue` supports expressions — `$json.email` binds per
  item — while `keyName` and the increment's `counterColumn` never do: a column
  name is resolved against the catalogue, not taken from an item
  (nodes/datastore.go).

## Columns you may filter on

A name is resolved against the datastore's live columns plus the three system
columns the editor offers: `id`, `createdAt`, `updatedAt`. Matching is
case-insensitive and the catalogue's spelling is what gets quoted, so
`createdat` and `createdAt` are one column on both drivers
(`resolveFilterColumn`). A name that belongs to no column of *this* datastore is
rejected before any SQL text is built: quoting is the second line of defence,
never the first. `dryRunState` is reserved but not physical, so filtering on it
is the reserved-word error rather than unknown column. System columns are
readable and never writable (internal/datastore/idents.go).

## Match mode and emptiness

`match` defaults to `any` — n8n's Any Condition — which becomes `or`
(`NodeMatchToFilterType`). The empty filter is not a wildcard:
`{"type":"and","filters":[]}` and an absent `filters` key decode to the same
thing, so a write carrying either is refused 422 with "an empty filter matches
nothing by refusal, never everything" (`refuseEmptyFilter` in
internal/api/handlers/datastores.go). A read is allowed to carry no filter; it
reads the page.

## Through the escape hatch

`list-datastore-rows` takes the filter as repeated query parameters, zipped by
position, and they must line up — a ragged list is a 422 rather than a silent
truncation (`queryFilter`):

```
kilasflow api list-datastore-rows --path id=ds_01J8ZP \
  --query match=all \
  --query columnName=status --query condition=eq --query value=active \
  --query columnName=score  --query condition=gte --query value=10
```

The same envelope is the `filter` object of a write's JSON body, reached with
`kilasflow api` and `--body @file.json`: `update.json` is
`{"filter": …, "values": {…}}`, `delete.json` is
`{"filter": …, "ifUpdatedAt": "…"}` and `increment.json` is
`{"filter": …, "column": "count", "amount": 1}`
(internal/api/handlers/datastores.go).

## Paging

Row listings are keyset, not offset: `limit` defaults to 25 and clamps at 100,
and the `cursor` pins the last id seen, so a row inserted while you page lands
past every cursor you hold and cannot shift rows onto a page already read. A
cursor from another format is a 400, never a row offset (`DefaultRowPageSize`,
`MaxRowPageSize`, `encodeRowCursor` in internal/datastore/filter.go). The CLI's
`--limit`/`--cursor` are that pair; the node's `Return All` bypasses both by
design, and its `limitPerInputRow` (default 50) reads that many rows per item.

## The agent tool's filters

`kilasflow.datastoreTool` compiles the same ten operators but accepts none of
the surprises: its schema is closed over the frozen column list of the one table
it bound, a filter row is a column plus an operator plus a value, and the
vocabulary is `eq`, `neq`, `like`, `ilike`, `gt`, `gte`, `lt`, `lte`, `isEmpty`,
`isNotEmpty`. The schema is a hint to the model, so every column and operator is
re-checked before any statement is built, and a call naming anything else is
refused with the names it may use instead (nodes/datastore.go).
