// src/pages/Dashboard.tsx
import React, { useState } from "react";
import { Table, Card, Button, message, theme, Input, Select, Switch, Space, Alert } from "antd";
import {
  StopOutlined,
  EyeOutlined,
  CheckSquareOutlined,
  ReloadOutlined,
  CheckCircleOutlined,
  ExclamationCircleOutlined,
  CloseCircleOutlined,
  DisconnectOutlined,
  SyncOutlined,
  CloseOutlined,
  ApiOutlined
} from "@ant-design/icons";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useDomains } from "../hooks/useDomains";
import { updateDomainSettings, scanSingleDomain, batchScanDomains, batchUpdateSettings } from "../services/api";
import { getDomainColumns } from "../components/DomainList/columns";
import { DomainDetailDrawer } from "../components/DomainList/DomainDetailDrawer";
import { EditDomainModal } from "../components/DomainList/EditDomainModal";
import DashboardCharts from "../components/DashboardCharts"; // 假設這檔案已存在
import type { SSLCertificate } from "../types";

// [Tip] 可將篩選列再拆分成 <DomainFilterBar />
interface Props {
  ignoredFilter: string;
  showCharts?: boolean;
  isDarkMode?: boolean;
}

const Dashboard: React.FC<Props> = ({ ignoredFilter, showCharts, isDarkMode }) => {
  const { token } = theme.useToken();
  const queryClient = useQueryClient();

  // 1. 使用 Hook 獲取資料與狀態
  const domainState = useDomains(1, 10, ignoredFilter);

  // 2. 本地 UI 狀態 (Modal/Drawer 開關)
  const [detailDrawerOpen, setDetailDrawerOpen] = useState(false);
  const [editModalOpen, setEditModalOpen] = useState(false);
  const [currentRecord, setCurrentRecord] = useState<SSLCertificate | null>(null);
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([]);

  // 3. Mutations (更新操作)
  const toggleIgnoreMutation = useMutation({
    mutationFn: ({ id, val }: { id: string; val: boolean }) => updateDomainSettings(id, val),
    onSuccess: () => {
      message.success("設定已更新");
      queryClient.invalidateQueries({ queryKey: ["domains"] });
    },
  });

  // 2. [新增] 完整更新設定 (包含 Port)
  const updateSettingsMutation = useMutation({
    mutationFn: ({ id, values }: { id: string; values: { is_ignored: boolean; port: number } }) =>
      updateDomainSettings(id, values.is_ignored, values.port),
    onSuccess: () => {
      message.success("設定 (Port/狀態) 已更新並儲存");
      setEditModalOpen(false); // 關閉 Modal
      queryClient.invalidateQueries({ queryKey: ["domains"] }); // 重新整理列表
    },
    onError: (err) => {
      message.error("更新失敗");
      console.error(err);
    }
  });

  const scanSingleMutation = useMutation({
    mutationFn: scanSingleDomain,
    onSuccess: () => {
      message.success("掃描請求已發送");
      queryClient.invalidateQueries({ queryKey: ["domains"] });
    },
  });

  // 4. 定義操作 Handler
  const handleDetail = (record: SSLCertificate) => {
    setCurrentRecord(record);
    setDetailDrawerOpen(true);
  };

  // [新增] 點擊編輯按鈕時
  const handleEdit = (record: SSLCertificate) => {
    setCurrentRecord(record);
    setEditModalOpen(true);
  };

  const handleEditSubmit = (id: string, values: { is_ignored: boolean; port: number }) => {
    updateSettingsMutation.mutate({ id, values });
  };
  // 輔助函式：判斷該行是否正在 Loading (避免整張表都在轉圈)
  const isIgnoreLoading = (id: string) =>
    toggleIgnoreMutation.isPending && toggleIgnoreMutation.variables?.id === id;

  // 批量更新 Mutation
  const batchSettingsMutation = useMutation({
    mutationFn: ({ ids, isIgnored }: { ids: string[]; isIgnored: boolean }) =>
      batchUpdateSettings(ids, isIgnored),
    onSuccess: (_, variables) => {
      const action = variables.isIgnored ? "批量停止監控" : "批量開啟監控";
      message.success(`${action}成功 (${variables.ids.length} 筆)`);
      setSelectedRowKeys([]); // 清空選取
      queryClient.invalidateQueries({ queryKey: ["domains"] });
    },
    onError: () => {
      message.error("批量更新失敗");
    }
  });

  // [新增] 批量掃描 Mutation
  const batchScanMutation = useMutation({
    mutationFn: (ids: string[]) => batchScanDomains(ids),
    onSuccess: (_, ids) => {
      message.success(`已觸發批量掃描 (${ids.length} 筆)，請稍後重新整理查看結果`);
      setSelectedRowKeys([]); // 清空選取
      // 掃描通常是背景非同步任務，立即 invalidate 可以讓狀態變為 pending (視後端實作而定)
      queryClient.invalidateQueries({ queryKey: ["domains"] });
    },
    onError: () => {
      message.error("批量掃描請求失敗");
    }
  });

  // [新增] 批量操作處理函式
  const handleBatchUpdate = (isIgnored: boolean) => {
    if (selectedRowKeys.length === 0) return;
    // 將 React.Key[] 轉為 string[]
    const ids = selectedRowKeys.map(k => k.toString());
    batchSettingsMutation.mutate({ ids, isIgnored });
  };

  // [新增] 批量掃描處理函式
  const handleBatchScan = () => {
    if (selectedRowKeys.length === 0) return;
    const ids = selectedRowKeys.map(k => k.toString());
    batchScanMutation.mutate(ids);
  };

  // [新增] 表格選取設定
  const rowSelection = {
    selectedRowKeys,
    onChange: (newSelectedRowKeys: React.Key[]) => {
      setSelectedRowKeys(newSelectedRowKeys);
    },
  };

  // 5. 獲取表格 Columns 設定
  const columns = getDomainColumns({
    onToggleIgnore: (id, val) => toggleIgnoreMutation.mutate({ id, val }),
    onEdit: handleEdit, // 這裡可接 EditModal
    onScan: (id) => scanSingleMutation.mutate(id),
    onDetail: handleDetail,
    ignoreLoading: toggleIgnoreMutation.isPending
  });

  return (
    <div>
      {showCharts && <DashboardCharts stats={domainState.stats} loading={domainState.statsLoading} isDarkMode={isDarkMode} />}

      <Card bordered={false} style={{ borderRadius: "8px", marginTop: 16 }}>
        {/* 簡單的 Filter Bar (建議進一步封裝) */}
        <div style={{ marginBottom: 16, display: "flex", gap: 16, flexWrap: "wrap", background: token.colorFillAlter, padding: 12, borderRadius: 4 }}>
          <Input.Search
            placeholder="搜尋域名..."
            style={{ width: 200 }}
            onSearch={(val) => { domainState.setSearchText(val); domainState.setPage(1); }}
            allowClear
          />
          <Select
            placeholder="主域名 (Zone)"
            style={{ width: 200 }}
            allowClear
            showSearch // 開啟搜尋功能，方便從長列表中快速找到
            value={domainState.selectedZone}
            onChange={(val) => {
              domainState.setSelectedZone(val);
              domainState.setPage(1); // 切換篩選時重置回第一頁
            }}
            // 假設 API 回傳的是 string[]，將其轉為 options 格式
            // options={domainState.zones?.data?.map((z: string) => ({ label: z, value: z })) || []}
            // 或是如果您 useDomains 直接回傳 array，則用:
            options={domainState.zones?.map((z: string) => ({ label: z, value: z }))}
          />

         <Select
            placeholder="狀態"
            style={{ width: 140 }}
            allowClear
            onChange={domainState.setSelectedStatus}
            options={[
              {
                label: (
                  <span>
                    <CheckCircleOutlined style={{ color: '#52c41a', marginRight: 8 }} />
                    正常 (Active)
                  </span>
                ),
                value: 'active',
              },
              {
                label: (
                  <span>
                    <ExclamationCircleOutlined style={{ color: '#faad14', marginRight: 8 }} />
                    即將過期 (Warning)
                  </span>
                ),
                value: 'warning',
              },
              {
                label: (
                  <span>
                    <CloseCircleOutlined style={{ color: '#ff4d4f', marginRight: 8 }} />
                    已過期 (Expired)
                  </span>
                ),
                value: 'expired',
              },
              {
                label: (
                  <span>
                    <DisconnectOutlined style={{ color: '#ff4d4f', marginRight: 8 }} />
                    無法解析 (Error)
                  </span>
                ),
                value: 'unresolvable',
              },
              {
                label: (
                  <span>
                    <ApiOutlined style={{ color: '#cf1322', marginRight: 8 }} />
                    連線錯誤 (Conn_Error)
                  </span>
                ),
                value: 'connection_error',
              },
              {
                label: (
                  <span>
                    <SyncOutlined style={{ color: '#1890ff', marginRight: 8 }} />
                    等待中 (Pending)
                  </span>
                ),
                value: 'pending',
              },
              {
                label: (
                  <span>
                    <CloseOutlined style={{ color: '#eb2f96', marginRight: 8 }} />
                    憑證錯誤 (Mismatch)
                  </span>
                ),
                value: 'mismatch',
              },
            ]}
          />
          <Space>
            <span>Proxy Only:</span>
            <Switch checked={domainState.onlyProxied} onChange={domainState.setOnlyProxied} />
          </Space>
        </div>

        {/* [新增] 批量操作工具列 (當有選取時顯示) */}
        {selectedRowKeys.length > 0 && (
          <Alert
            style={{ marginBottom: 16 }}
            type="info"
            message={
              <Space>
                <CheckSquareOutlined />
                <span>已選擇 {selectedRowKeys.length} 項</span>
                <div style={{ width: 1, height: 16, background: token.colorBorder, margin: '0 8px' }} />


                {/* [新增] 批量掃描按鈕 */}
                <Button
                  size="small"
                  // 使用預設樣式或藍色系
                  icon={<ReloadOutlined />}
                  loading={batchScanMutation.isPending}
                  onClick={handleBatchScan}
                >
                  批量掃描
                </Button>

                <Button
                  size="small"
                  type="primary" // 綠色系通常代表開啟/正常
                  style={{ backgroundColor: '#52c41a', borderColor: '#52c41a' }}
                  icon={<EyeOutlined />}
                  loading={batchSettingsMutation.isPending}
                  onClick={() => handleBatchUpdate(false)}
                >
                  開啟監控
                </Button>

                <Button
                  size="small"
                  danger // 紅色系代表危險/停止
                  icon={<StopOutlined />}
                  loading={batchSettingsMutation.isPending}
                  onClick={() => handleBatchUpdate(true)}
                >
                  關閉監控 (忽略)
                </Button>

                <Button size="small" type="text" onClick={() => setSelectedRowKeys([])}>
                  取消選取
                </Button>
              </Space>
            }
          />
        )}

        {/* 表格本體 */}
        <Table
          rowKey="id"
          rowSelection={rowSelection}
          columns={columns}
          dataSource={domainState.domainsData?.data}
          loading={domainState.loading}
          pagination={{
            current: domainState.page,
            pageSize: domainState.pageSize,
            total: domainState.domainsData?.total,
            onChange: (p, ps) => { domainState.setPage(p); domainState.setPageSize(ps); }
          }}
          onChange={(_pag, _filt, sorter: any) => {
            // 1. 如果使用者取消排序 (sorter.order 為 undefined)，重置為空字串或預設排序
            if (!sorter.order) {
              domainState.setSortField("");
              return;
            }

            // 2. 針對 SSL 剩餘天數排序
            if (sorter.field === "days_remaining") {
              domainState.setSortField(sorter.order === "ascend" ? "expiry_asc" : "expiry_desc");
            }
            // 3. [新增] 針對 網域註冊到期日 排序
            else if (sorter.field === "domain_days_left") {
              domainState.setSortField(sorter.order === "ascend" ? "domain_expiry_asc" : "domain_expiry_desc");
            }
            // 4. [新增] 針對 上次檢查時間 排序 (如果您的 Columns 有開 sorter)
            else if (sorter.field === "last_check_time") {
              domainState.setSortField(sorter.order === "ascend" ? "check_time_asc" : "check_time_desc");
            }
          }}
        />
      </Card>

      {/* 詳情 Drawer */}
      <DomainDetailDrawer
        open={detailDrawerOpen}
        onClose={() => setDetailDrawerOpen(false)}
        record={currentRecord}
      />

      {/* [新增] 編輯 Modal */}
      <EditDomainModal
        open={editModalOpen}
        record={currentRecord}
        onClose={() => setEditModalOpen(false)}
        onSubmit={handleEditSubmit}
        confirmLoading={updateSettingsMutation.isPending}
      />
    </div>
  );
};

export default Dashboard;