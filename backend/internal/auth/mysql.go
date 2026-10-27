package auth

import (
	"context"
	"errors"
	"strconv"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type userRecord struct {
	ID       uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	Phone    string `gorm:"column:phone"`
	NickName string `gorm:"column:nick_name"`
	Icon     string `gorm:"column:icon"`
}

func (userRecord) TableName() string { return "tb_user" }

type mysqlUsers struct{ db *gorm.DB }

func (store mysqlUsers) FindOrCreate(ctx context.Context, phone, nickName string) (User, error) {
	db := store.db.WithContext(ctx)
	var record userRecord
	err := db.Where("phone = ?", phone).Take(&record).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		record = userRecord{Phone: phone, NickName: nickName, Icon: ""}
		// MySQL's phone unique key arbitrates concurrent first logins. DoNothing
		// leaves the winner's ID/profile intact; always reread the winning row.
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
			return User{}, err
		}
		record = userRecord{}
		if err := db.Where("phone = ?", phone).Take(&record).Error; err != nil {
			return User{}, err
		}
	}
	if record.ID == 0 {
		return User{}, errUnavailable
	}
	return User{ID: strconv.FormatUint(record.ID, 10), NickName: record.NickName, Icon: record.Icon}, nil
}
