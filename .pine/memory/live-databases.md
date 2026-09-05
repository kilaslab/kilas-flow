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
docker run -d --name kf-mar -e MARIADB_ROOT_PASSWORD=hunter2 -e MARIADB_DATABASE=kilasflow \
  -e MARIADB_USER=kilas -e MARIADB_PASSWORD=hunter2 -p 55435:3306 mariadb:11

# wait: docker exec kf-pg  pg_isready -U kilas -d kilasflow
#       docker exec kf-my  mysqladmin ping -ukilas -phunter2
#       docker exec kf-mar mariadb-admin ping -ukilas -phunter2

KILASFLOW_TEST_POSTGRES_DSN="postgres://kilas:hunter2@127.0.0.1:55433/kilasflow?sslmode=disable" \
KILASFLOW_TEST_MYSQL_DSN="mysql://kilas:hunter2@127.0.0.1:55434/kilasflow" \
KILASFLOW_TEST_MARIADB_DSN="mysql://kilas:hunter2@127.0.0.1:55435/kilasflow" \
  go test ./... -count=1

docker rm -f kf-pg kf-my kf-mar
```

Ports are deliberately not 5432/3306, so a run never reaches a server the
developer is already using.

MariaDB is a separate gate because it and MySQL differ on exactly the two
decisions the SQL builder makes for them: MariaDB has `INSERT … RETURNING` and
lacks MySQL 8.0.19's row-alias upsert, so a change that only ever runs against
one of them can be wrong on the other and pass. Its DSN uses the `mysql://`
scheme, because the driver and the credential type are the same.

`KILASFLOW_TEST_MYSQL_DSN` was added by FEAT-9dqn7d and `KILASFLOW_TEST_MARIADB_DSN` by FEAT-12s0e5. Both gates take a URL:
`internal/database` passes it to GORM, and `nodes/database_test.go` takes it
apart into the credential fields a database node actually accepts — a node never
sees a DSN, which is what stops a workflow naming a connection string of its own.
