package model

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/IBM/cloudland/web/sca/dbs"
)

type Interface struct {
	Model
	Name       string `gorm:"type:varchar(32)"`
	MacAddr    string `gorm:"type:varchar(32)"`
	InstanceID int64
	Device     int64
	Address    *Address `gorm:"foreignkey:Interface"`
	Hyper      int32    `gorm:"default:-1"`
	Primary    bool     `gorm:"default:false"`
	Type       string
	Mtu        int32
}

func init() {
	dbs.AutoMigrate(&Interface{})
	rand.Seed(time.Now().UnixNano())
}

func genMacaddr() (mac string, err error) {
	buf := make([]byte, 4)
	_, err = rand.Read(buf)
	if err != nil {
		log.Println("Failed to generate random numbers, %v", err)
		return
	}
	mac = fmt.Sprintf("52:54:%02x:%02x:%02x:%02x", buf[0], buf[1], buf[2], buf[3])
	return mac, nil
}

func CreateInterface(subnetID, ID int64, ifaceName, ifType string) (iface *Interface, err error) {
	db := dbs.DB()
	primary := false
	if ifaceName == "eth0" {
		primary = true
	}
	mac, err := genMacaddr()
	if err != nil {
		log.Println("Failed to generate random Mac address, %v", err)
		return
	}
	iface = &Interface{
		Name:    ifaceName,
		MacAddr: mac,
		Primary: primary,
		Type:    ifType,
		Mtu:     1450,
	}
	if ifType == "instance" {
		iface.InstanceID = ID
	} else {
		iface.Device = ID
	}
	err = db.Create(iface).Error
	if err != nil {
		log.Println("Failed to create interface, %v", err)
		return
	}
	iface.Address, err = AllocateAddress(subnetID, iface.ID, "native")
	if err != nil {
		log.Println("Failed to allocate address, %v", err)
		return
	}
	return iface, nil
}

func DeleteInterfaces(masterID int64, ifType string) (err error) {
	db := dbs.DB()
	ifaces := []*Interface{}
	if ifType == "instance" {
		err = db.Where("instance_id = ? and type = ?", masterID, "instance").Find(&ifaces).Error
	} else {
		err = db.Where("device = ? and type like ?", masterID, "%gateway%").Find(&ifaces).Error
	}
	if err != nil {
		log.Println("Failed to query interfaces, %v", err)
		return
	}
	err = DeallocateAddress(ifaces)
	if err != nil {
		log.Println("Failed to deallocate address, %v", err)
		return
	}
	if ifType == "instance" {
		err = db.Where("instance_id = ? and type = ?", masterID, "instance").Delete(&Interface{}).Error
	} else {
		err = db.Where("device = ? and type like ?", masterID, "%gateway%").Delete(&Interface{}).Error
	}
	if err != nil {
		log.Println("Failed to delete interface, %v", err)
		return
	}
	return
}
