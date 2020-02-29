# mysql-manticore

Connects to MySQL/MariaDB via the replication protocol, and sends updates to [Manticore](https://manticoresearch.com/).

## Usage

1. Installation
    
    Docker images are available on
    -  [Docker Hub](https://hub.docker.com/r/gleez/mysql-manticore)
    -  [Github](https://github.com/sandeepone/mysql-manticore/packages)

2. Configure MySQL/MariaDB

    - `binlog_format` must be `ROW`
    - `binlog_row_image` must be `FULL` (otherwise you may not be able to determine which documents should be updated)

3. Configure Manticore

    - index `sync_state` should exist and have the following structure

            type = rt
            rt_attr_string = binlog_name
            rt_attr_uint   = binlog_position
            rt_attr_string = gtid
            rt_attr_string = flavor

    - for every RT-index there should be a plain index with `_plain` suffix
    - if an index is partitioned, then its parts should have the same schema and names that end with `_part_0`, `_part_1`, etc.

4. Configure mysql-manticore

    See example config file [river.toml](./etc/river.toml).

5. To launch a Mysql Manticore container run

    ```docker run --name mysql-manticore -p 8080:8080 -d gleez/mysql-manticore```