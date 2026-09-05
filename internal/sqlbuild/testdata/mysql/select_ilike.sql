SELECT * FROM `customers` WHERE LOWER(`email`) LIKE LOWER(?)
-- parameters: ["ADA@%"]
-- returning
