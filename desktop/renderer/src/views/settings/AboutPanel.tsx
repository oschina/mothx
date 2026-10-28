// 关于面板:版本、平台与 ACP 运行时信息。

import { Cpu } from 'lucide-react';

import { RowItem, RowList } from '@/components/layout';
import { useAppSelector } from '@/hooks/useAppState';

export function AboutPanel() {
  const info = useAppSelector((state) => state.appInfo);
  const detail = `v${info.version} · ${info.platform}/${info.arch} · ACP v1${info.runtimeBinary ? ` · ${info.runtimeBinary}` : ''}`;
  return (
    <RowList>
      <RowItem icon={<Cpu />} title="MothX Desktop" desc={detail} />
    </RowList>
  );
}
