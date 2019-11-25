package river

import (
	"fmt"

	"github.com/juju/errors"
	"github.com/siddontang/go-mysql/mysql"
	"github.com/superjobru/go-mysql-sphinx/sphinx"
	"github.com/superjobru/go-mysql-sphinx/util"
)

func getDBValueExpression(result *mysql.Result, fieldType string, rowNo int, colNo int) (string, error) {
	switch fieldType {
	case DocID:
		return formatDocID(result, rowNo, colNo)
	case AttrFloat:
		return formatFloat(result, rowNo, colNo)
	case AttrUint:
		return formatUint(result, rowNo, colNo)
	case AttrBigint:
		return formatBigint(result, rowNo, colNo)
	case AttrString, TextField:
		return formatString(result, rowNo, colNo)
	case AttrMulti, AttrMulti64:
		return formatMulti(result, rowNo, colNo)
	case AttrBool:
		return formatBool(result, rowNo, colNo)
	case AttrTimestamp:
		return formatTimestamp(result, rowNo, colNo)
	default:
		return "", errors.Errorf("somehow got invalid '%s' type from config", fieldType)
	}
}

func formatDocID(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetUint(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}
	return fmt.Sprintf("%d", val), nil
}

func formatFloat(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetFloat(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}
	return fmt.Sprintf("%f", val), nil
}

func formatUint(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetValue(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}
	typed, err := util.CoerceToUint32(val)
	if err != nil {
		return "", errors.Trace(err)
	}
	return fmt.Sprintf("%d", typed), nil
}

func formatBigint(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetValue(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}
	typed, err := util.CoerceToInt64(val)
	if err != nil {
		return "", errors.Trace(err)
	}
	return fmt.Sprintf("%d", typed), nil
}

func formatString(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetString(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}
	return sphinx.QuoteString(val), nil
}

func formatMulti(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetString(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}

	if val == "null" || val == "" {
		return "()", nil
	}

	return fmt.Sprintf("(%s)", val), nil
}

func formatBool(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetValue(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}
	return fmt.Sprintf("%d", val), nil
}

func formatTimestamp(result *mysql.Result, rowNo int, colNo int) (string, error) {
	val, err := result.GetFloat(rowNo, colNo)
	if err != nil {
		return "", errors.Trace(err)
	}

	return fmt.Sprintf("%.0f", val), nil
}
