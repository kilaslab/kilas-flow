# Live databases for integration-gated tests

Several tickets ask for coverage "run by hand with `KILASFLOW_TEST_POSTGRES_DSN`
set". Docker is available on this machine, so those runs do not have to be
deferred — spin a throwaway server up, run the gated test, record the output on
the ticket, tear it down.

```sh
docker run -d --name kf-pg -e POSTGRES_PASSWORD=hunter2 -e POSTGRES_USER=kilas \
  -e POSTGRES_DB=kilasflow -p 55433:5432 postgres:16-alpine
docker run -d --name kf-my -e MYSQL_ROOT_PASSWORD=hunter2 -e MYSQL_DATABASE=kilasflow \
  -e MYSQL_USER=kilas -e MYSQL_PASSWORD=hunter2 -p 55434:3306 mysql:8

# wait: docker exec kf-pg pg_isready -U kilas -d kilasflow
#       docker exec kf-my mysqladmin ping -ukilas -phunter2

KILASFLOW_TEST_POSTGRES_DSN="postgres://kilas:hunter2@127.0.0.1:55433/kilasflow?sslmode=disable" \
KILASFLOW_TEST_MYSQL_DSN="mysql://kilas:hunter2@127.0.0.1:55434/kilasflow" \
  go test ./... -count=1

docker rm -f kf-pg kf-my
```

Ports are deliberately not 5432/3306, so a run never reaches a server the
developer is already using.

`KILASFLOW_TEST_MYSQL_DSN` was added by FEAT-9dqn7d. Both gates take a URL:
`internal/database` passes it to GORM, and `nodes/database_test.go` takes it
apart into the credential fields a database node actually accepts — a node never
sees a DSN, which is what stops a workflow naming a connection string of its own.
