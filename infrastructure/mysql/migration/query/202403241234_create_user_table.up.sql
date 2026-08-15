CREATE TABLE IF NOT EXISTS `examples`
(
    `id`         BIGINT       NOT NULL AUTO_INCREMENT,
    `name`       VARCHAR(255) NOT NULL,
    `created_at` TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_examples_name` (`name`)
) ENGINE = InnoDB
  DEFAULT CHARSET = utf8mb4;
