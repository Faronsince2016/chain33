package dapp

import (
	"errors"
	"fmt"

	"gitlab.33.cn/chain33/chain33/types"
)

//通过addr前缀查找本地址参与的所有交易
//查询交易默认放到：coins 中查询
func (d *DriverBase) GetTxsByAddr(addr *types.ReqAddr) (types.Message, error) {
	db := d.GetLocalDB()
	var prefix []byte
	var key []byte
	var txinfos [][]byte
	var err error
	//取最新的交易hash列表
	if addr.Flag == 0 { //所有的交易hash列表
		prefix = CalcTxAddrHashKey(addr.GetAddr(), "")
	} else if addr.Flag > 0 { //from的交易hash列表
		prefix = CalcTxAddrDirHashKey(addr.GetAddr(), addr.Flag, "")
	} else {
		return nil, errors.New("flag unknown")
	}
	if addr.GetHeight() == -1 {
		txinfos, err = db.List(prefix, nil, addr.Count, addr.GetDirection())
		if err != nil {
			return nil, err
		}
		if len(txinfos) == 0 {
			return nil, errors.New("tx does not exist")
		}
	} else { //翻页查找指定的txhash列表
		blockheight := addr.GetHeight()*types.MaxTxsPerBlock + addr.GetIndex()
		heightstr := fmt.Sprintf("%018d", blockheight)
		if addr.Flag == 0 {
			key = CalcTxAddrHashKey(addr.GetAddr(), heightstr)
		} else if addr.Flag > 0 { //from的交易hash列表
			key = CalcTxAddrDirHashKey(addr.GetAddr(), addr.Flag, heightstr)
		} else {
			return nil, errors.New("flag unknown")
		}
		txinfos, err = db.List(prefix, key, addr.Count, addr.Direction)
		if err != nil {
			return nil, err
		}
		if len(txinfos) == 0 {
			return nil, errors.New("tx does not exist")
		}
	}
	var replyTxInfos types.ReplyTxInfos
	replyTxInfos.TxInfos = make([]*types.ReplyTxInfo, len(txinfos))
	for index, txinfobyte := range txinfos {
		var replyTxInfo types.ReplyTxInfo
		err := types.Decode(txinfobyte, &replyTxInfo)
		if err != nil {
			return nil, err
		}
		replyTxInfos.TxInfos[index] = &replyTxInfo
	}
	return &replyTxInfos, nil
}

//查询指定prefix的key数量，用于统计
func (d *DriverBase) GetPrefixCount(key *types.ReqKey) (types.Message, error) {
	var counts types.Int64
	db := d.GetLocalDB()
	counts.Data = db.PrefixCount(key.Key)
	return &counts, nil
}

//查询指定地址参与的交易计数，用于统计
func (d *DriverBase) GetAddrTxsCount(reqkey *types.ReqKey) (types.Message, error) {
	var counts types.Int64
	db := d.GetLocalDB()
	TxsCount, err := db.Get(reqkey.Key)
	if err != nil && err != types.ErrNotFound {
		counts.Data = 0
		return &counts, nil
	}
	if len(TxsCount) == 0 {
		counts.Data = 0
		return &counts, nil
	}
	err = types.Decode(TxsCount, &counts)
	if err != nil {
		counts.Data = 0
		return &counts, nil
	}
	return &counts, nil
}