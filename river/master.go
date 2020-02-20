package river

import (
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/juju/errors"

	"github.com/sandeepone/mysql-manticore/sphinx"
	"github.com/siddontang/go-mysql/mysql"
)

type masterState struct {
	sync.RWMutex
	flavor            string
	gtid              *mysql.GTIDSet
	pos               *mysql.Position
	useGTID           bool
	needPositionReset bool
	lastSaveTime      time.Time
	skipFileSyncState bool
}

type positionData struct {
	GTID  string `toml:"gtid"`
	Name  string `toml:"bin_name"`
	Pos   uint32 `toml:"bin_pos"`
	Reset bool   `toml:"reset"`
}

// MysqlPositionProvider used in resetToCurrent() method
type MysqlPositionProvider interface {
	GetMasterGTIDSet() (mysql.GTIDSet, error)
	GetMasterPos() (mysql.Position, error)
}

// newMasterState master state constructor
func newMasterState(c *Config) *masterState {
	var m = &masterState{useGTID: c.UseGTID, flavor: c.Flavor, skipFileSyncState: true}
	return m
}

func (m *masterState) updatePosition(posEvent positionEvent) bool {
	m.Lock()
	defer m.Unlock()

	var hasChanged bool

	if m.pos == nil {
		hasChanged = true
	} else if m.pos.Compare(posEvent.pos) != 0 {
		hasChanged = true
	}

	m.pos = &mysql.Position{
		Name: posEvent.pos.Name,
		Pos:  posEvent.pos.Pos,
	}

	if posEvent.gtid != nil {
		if m.gtid == nil {
			hasChanged = true
		} else if !(*m.gtid).Contain(posEvent.gtid) {
			hasChanged = true
		}
		m.gtid = &posEvent.gtid
	}

	return hasChanged
}

func (m *masterState) gtidSet() mysql.GTIDSet {
	m.Lock()
	defer m.Unlock()

	if m.gtid == nil {
		return nil
	}

	return (*m.gtid).Clone()
}

func (m *masterState) gtidString() string {
	m.Lock()
	defer m.Unlock()

	if m.gtid == nil {
		return ""
	}

	return (*m.gtid).String()
}

func (m *masterState) position() mysql.Position {
	m.Lock()
	defer m.Unlock()

	if m.pos == nil {
		return mysql.Position{}
	}

	return *m.pos
}

func (m *masterState) syncState() sphinx.SyncState {
	m.Lock()
	defer m.Unlock()

	var gtid mysql.GTIDSet
	var pos mysql.Position
	if m.gtid != nil {
		gtid = *m.gtid
	}
	if m.pos != nil {
		pos = *m.pos
	}

	return sphinx.SyncState{
		Position: pos,
		GTID:     gtid,
		Flavor:   m.flavor,
	}
}

func (m *masterState) String() string {
	return spew.Sprintf(
		"gtid=%v pos=%v useGTID=%v needPositionReset=%v",
		m.gtid,
		m.pos,
		m.useGTID,
		m.needPositionReset,
	)
}

func (m *masterState) resetToCurrent(p MysqlPositionProvider) error {
	m.Lock()
	defer m.Unlock()

	if m.useGTID {
		currentGTID, err := p.GetMasterGTIDSet()
		if err != nil {
			return errors.Trace(err)
		}
		m.gtid = &currentGTID
	}

	currentPos, err := p.GetMasterPos()
	if err != nil {
		return errors.Trace(err)
	}
	m.pos = &currentPos

	return nil
}
