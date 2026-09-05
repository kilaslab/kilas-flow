SELECT * FROM `customers` WHERE `tier` = ? AND `email` LIKE ? ORDER BY `email` ASC LIMIT 50
-- parameters: ["gold","%@example.test"]
-- returning
