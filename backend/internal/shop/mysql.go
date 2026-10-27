package shop

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

type mysqlStore struct {
	db       *gorm.DB
	location *time.Location
}

type shopRecord struct {
	ID         uint64
	Name       string
	TypeID     uint64
	Images     string
	Area       *string
	Address    string
	X          float64
	Y          float64
	AvgPrice   *uint64
	Sold       uint32
	Comments   uint32
	Score      uint32
	OpenHours  *string
	CreateTime *time.Time
	UpdateTime *time.Time
}

const shopColumns = "id, name, type_id, images, area, address, x, y, avg_price, sold, comments, score, open_hours, create_time, update_time"

func shopFilter(query Query) (string, []any) {
	where := " WHERE 1 = 1"
	arguments := make([]any, 0, 2)
	if query.Name != "" {
		// An explicit non-backslash escape works regardless of MySQL's
		// NO_BACKSLASH_ESCAPES setting. Search text is a literal substring.
		escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(query.Name)
		where += " AND name LIKE ? ESCAPE '!'"
		arguments = append(arguments, "%"+escaped+"%")
	}
	if query.TypeID != nil {
		where += " AND type_id = ?"
		arguments = append(arguments, *query.TypeID)
	}
	return where, arguments
}

// pageOffset compares against the actual row count before multiplying. An
// arbitrarily large valid current page returns empty without integer overflow.
func pageOffset(query Query, total uint64) (uint64, bool) {
	if query.Current == 0 || query.PageSize == 0 || total == 0 || query.Current-1 > (total-1)/query.PageSize {
		return 0, false
	}
	return (query.Current - 1) * query.PageSize, true
}

func (store mysqlStore) List(ctx context.Context, query Query) ([]Shop, uint64, error) {
	where, arguments := shopFilter(query)
	db := store.db.WithContext(ctx)
	var total uint64
	if err := db.Raw("SELECT COUNT(*) FROM tb_shop"+where, arguments...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	result := make([]Shop, 0)
	offset, present := pageOffset(query, total)
	if !present {
		return result, total, nil
	}
	var records []shopRecord
	pageArguments := append(arguments, query.PageSize, offset)
	if err := db.Raw("SELECT "+shopColumns+" FROM tb_shop"+where+" ORDER BY id ASC LIMIT ? OFFSET ?", pageArguments...).Scan(&records).Error; err != nil {
		return nil, 0, err
	}
	for _, record := range records {
		result = append(result, store.shop(record))
	}
	return result, total, nil
}

func (store mysqlStore) Detail(ctx context.Context, id uint64) (*Shop, error) {
	var record shopRecord
	err := store.db.WithContext(ctx).Table("tb_shop").Select(shopColumns).Where("id = ?", id).Take(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := store.shop(record)
	return &result, nil
}

func (store mysqlStore) shop(record shopRecord) Shop {
	var price *string
	if record.AvgPrice != nil {
		value := strconv.FormatUint(*record.AvgPrice, 10)
		price = &value
	}
	return Shop{
		ID: strconv.FormatUint(record.ID, 10), Name: record.Name,
		TypeID: strconv.FormatUint(record.TypeID, 10), Images: record.Images,
		Area: record.Area, Address: record.Address, X: record.X, Y: record.Y,
		AvgPrice: price, Sold: record.Sold, Comments: record.Comments, Score: record.Score,
		OpenHours: record.OpenHours, CreateTime: store.localTime(record.CreateTime), UpdateTime: store.localTime(record.UpdateTime),
	}
}

func (store mysqlStore) Types(ctx context.Context) ([]ShopType, error) {
	var records []struct {
		ID   uint64
		Name *string
		Icon *string
		Sort int32
	}
	if err := store.db.WithContext(ctx).Table("tb_shop_type").Select("id, name, icon, sort").Order("sort ASC, id ASC").Scan(&records).Error; err != nil {
		return nil, err
	}
	result := make([]ShopType, 0, len(records))
	for _, record := range records {
		result = append(result, ShopType{ID: strconv.FormatUint(record.ID, 10), Name: record.Name, Icon: record.Icon, Sort: record.Sort})
	}
	return result, nil
}

func (store mysqlStore) Vouchers(ctx context.Context, shopID uint64) ([]Voucher, error) {
	var records []struct {
		ID          uint64
		ShopID      uint64
		Title       string
		SubTitle    *string
		Rules       *string
		PayValue    uint64
		ActualValue int64
		Type        uint8
		Stock       int32
		BeginTime   *time.Time
		EndTime     *time.Time
	}
	// Preserve the Java selection: ACTIVE publication is required even for a
	// normal voucher. Stock and time do not filter public activity visibility.
	const query = `SELECT v.id, v.shop_id, v.title, v.sub_title, v.rules,
		v.pay_value, v.actual_value, v.type, sv.stock, sv.begin_time, sv.end_time
		FROM tb_voucher v JOIN tb_seckill_voucher sv ON sv.voucher_id = v.id
		WHERE v.shop_id = ? AND v.status = 1 AND sv.publish_status = 'ACTIVE'
		ORDER BY v.id ASC`
	if err := store.db.WithContext(ctx).Raw(query, shopID).Scan(&records).Error; err != nil {
		return nil, err
	}
	result := make([]Voucher, 0, len(records))
	for _, record := range records {
		result = append(result, Voucher{
			ID: strconv.FormatUint(record.ID, 10), ShopID: strconv.FormatUint(record.ShopID, 10),
			Title: record.Title, SubTitle: record.SubTitle, Rules: record.Rules,
			PayValue: strconv.FormatUint(record.PayValue, 10), ActualValue: strconv.FormatInt(record.ActualValue, 10),
			Type: record.Type, Stock: record.Stock,
			BeginTime: store.localTime(record.BeginTime), EndTime: store.localTime(record.EndTime),
		})
	}
	return result, nil
}

func (store mysqlStore) localTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.In(store.location).Format("2006-01-02T15:04:05.999999999")
	return &formatted
}
