// Package httpx 提供 /api/* 管理/站点接口的统一响应与分页助手。
// 约定见 docs/ai/api-conventions.md:信封 {code,msg,data};分页 page/pageSize;
// 列表 data:{list,page,pageSize,total}。/v1/* 网关代理不使用本包(走 utils.WriteOpenAIError)。
package httpx

import (
	"encoding/json"
	"net/http"
	"strconv"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// envelope 是统一返回信封。
type envelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

// PageData 是分页列表的标准 data 结构。
type PageData struct {
	List     any   `json:"list"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	Total    int64 `json:"total"`
}

func write(w http.ResponseWriter, httpStatus, code int, msg string, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(envelope{Code: code, Msg: msg, Data: data})
}

// OK 返回成功(HTTP 200, code 0)。
func OK(w http.ResponseWriter, data any) {
	write(w, http.StatusOK, 0, "success", data)
}

// Page 返回分页列表(HTTP 200, code 0, data:{list,page,pageSize,total})。
// list 为 nil 时输出空数组,避免 null。
func Page(w http.ResponseWriter, list any, page, pageSize int, total int64) {
	if list == nil {
		list = []any{}
	}
	OK(w, PageData{List: list, Page: page, PageSize: pageSize, Total: total})
}

// Err 返回错误(指定 HTTP 状态 + 业务 code + msg, data 为 null)。
// code 传 0 时按 httpStatus 兜底,保证非成功响应 code 必非 0。
func Err(w http.ResponseWriter, httpStatus, code int, msg string) {
	if code == 0 {
		code = httpStatus
	}
	write(w, httpStatus, code, msg, nil)
}

// ParsePage 解析 page / pageSize(默认 1 / 20,pageSize 上限 100),并返回 offset。
func ParsePage(r *http.Request) (page, pageSize, offset int) {
	page, pageSize = 1, defaultPageSize
	if raw := r.URL.Query().Get("page"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			page = v
		}
	}
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			if v > maxPageSize {
				v = maxPageSize
			}
			pageSize = v
		}
	}
	return page, pageSize, (page - 1) * pageSize
}
