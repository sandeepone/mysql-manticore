module github.com/sandeepone/mysql-manticore

go 1.13

require (
	github.com/BurntSushi/toml v0.3.1
	github.com/birkirb/loggers-mapper-logrus v0.0.0-20180326232643-461f2d8e6f72
	github.com/davecgh/go-spew v1.1.1
	github.com/deckarep/golang-set v0.0.0-20171013212420-1d4478f51bed
	github.com/gleez/pkg v0.0.0-20200218023659-166e6bcfab6c // indirect
	github.com/gocarina/gocsv v0.0.0-20191122093448-c6a9c812ac26
	github.com/juju/errors v0.0.0-20170703010042-c7d06af17c68
	github.com/kr/pretty v0.1.0
	github.com/kr/text v0.1.0
	github.com/robfig/cron v1.2.0
	github.com/sandeepone/sqlparser v0.0.0-20191206073631-108f7fcbf8a5
	github.com/satori/go.uuid v1.2.0
	github.com/shopspring/decimal v0.0.0-20180709203117-cd690d0c9e24
	github.com/siddontang/go v0.0.0-20180604090527-bdc77568d726
	github.com/siddontang/go-log v0.0.0-20180807004314-8d05993dda07
	github.com/siddontang/go-mysql v0.0.0-20200120044259-a9add8d89449
	github.com/siddontang/loggers v1.0.4-0.20180516082531-fa51471f8169
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.4.0
	github.com/thejerf/suture v3.0.2+incompatible
	github.com/xwb1989/sqlparser v0.0.0-20180606152119-120387863bf2
	golang.org/x/crypto v0.0.0-20200214034016-1d94cc7ab1c6
	golang.org/x/sys v0.0.0-20190926180325-855e68c8590b
	gopkg.in/birkirb/loggers.v1 v1.1.0
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15
)

replace gopkg.in/russross/blackfriday.v2 v2.0.1 => github.com/russross/blackfriday/v2 v2.0.1
