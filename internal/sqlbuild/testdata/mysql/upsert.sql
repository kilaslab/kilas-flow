INSERT INTO `customers` (`email`, `id`, `tier`) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE `email` = VALUES(`email`), `tier` = VALUES(`tier`)
-- parameters: ["ada@example.test",7,"gold"]
