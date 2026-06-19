package blueprint

import (
	"time"

	"github.com/uptrace/bun"
)

type Blueprint struct {
	bun.BaseModel `bun:"table:blueprints"`

	ID        string    `bun:"id,pk,type:uuid"`
	CreatedAt time.Time `bun:"created_at,notnull,default:now()"`
	UpdatedAt time.Time `bun:"updated_at,notnull,default:now()"`

	Name string `bun:"name,notnull"`
}
