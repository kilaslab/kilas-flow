-- Reverse of 000005_datastores.up.sql.
--
-- The columns go with their datastore, so only the tables are named. The
-- physical tables the engine created at run time are not touched here: they
-- are dropped through the engine's Drop, which removes the catalogue rows
-- and the physical table together.

DROP TABLE `datastore_columns`;

DROP TABLE `datastores`;
