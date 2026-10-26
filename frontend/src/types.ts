export interface Result<T> {
  success: boolean
  errorMsg?: string
  data: T
  total?: number | string
}

export interface User {
  id: string
  nickName: string
  icon?: string
}

export interface Shop {
  id: string
  name: string
  typeId?: string
  images?: string
  area?: string
  address?: string
  avgPrice?: number | string
  sold?: number
  comments?: number
  score?: number
  openHours?: string
}

export interface ShopType {
  id: string
  name?: string
  icon?: string
  sort: number
}

export interface Voucher {
  id: string
  shopId: string
  title: string
  subTitle?: string
  rules?: string
  payValue: number | string
  actualValue: number | string
  type: number
  stock?: number
  beginTime?: string
  endTime?: string
}

export type OrderStatusValue = 1 | 2 | 3 | 4 | 6

export interface OrderStatus {
  id: string
  state: 'PROCESSING' | 'CREATED' | 'FAILED'
  voucherId?: string
  failureReason?: string
  errorMsg?: string
  message?: string
  orderStatus?: OrderStatusValue | null
  shopId?: string
  payAmount?: string
  payDeadlineAt?: string
  redeemBeginTime?: string
  redeemEndTime?: string
  redeemToken?: string
  createTime?: string
  payTime?: string
  useTime?: string
  refundTime?: string
}

export interface MerchantWorkspace {
  merchants: {
    id: string
    name: string
    role: 'OWNER' | 'MANAGER' | 'STAFF'
    shops: { id: string; name: string; address: string }[]
  }[]
}

export interface Activity {
  id: string
  shopId: string
  shopName: string
  title: string
  subTitle?: string
  payValue: string
  actualValue: string
  totalStock: number
  stock: number
  publishStatus: 'INITIALIZING' | 'ACTIVE' | 'OFFLINE'
  publishVersion: number
  ready: boolean
  beginTime: string
  endTime: string
  redeemBeginTime?: string
  redeemEndTime?: string
}

export interface CreateActivityRequest {
  shopId: string
  title: string
  subTitle: string | null
  rules: string | null
  payValue: string
  actualValue: string
  totalStock: number
  beginTime: string
  endTime: string
  redeemBeginTime: string
  redeemEndTime: string
}

export interface MerchantOrder {
  id: string
  shopId: string
  shopName: string
  userId: string
  userPhone: string
  voucherId: string
  voucherTitle: string
  orderStatus: OrderStatusValue
  payAmount: string
  createTime: string
  payTime?: string
  useTime?: string
  refundTime?: string
  redeemEndTime?: string
}

export interface RedeemResult {
  order: MerchantOrder
  alreadyRedeemed: boolean
}

export interface AuditLog {
  id: string
  shopId?: string
  operatorUserId: string
  operatorType: 'USER' | 'MERCHANT' | 'SYSTEM'
  action: string
  resourceId?: string
  detail?: string
  createTime: string
}

export interface AiCapabilities {
  enabled: boolean
  ready: boolean
  mode: string
  reason: string
  chatModel: string
  embeddingModel: string
  embeddingDimensions: number
}

export interface AiSource {
  chunkId: string
  documentId: string
  version: number
  title: string
  scope: string
  shopId?: string
  excerpt: string
  href: string
  score?: number
}

export interface AiToolCard {
  type: 'order' | 'orders' | 'activities' | 'shops'
  items?: Record<string, unknown>[]
  [key: string]: unknown
}

export interface AiMessageView {
  role: 'user' | 'assistant'
  text: string
  time: string
  citationIds: string[]
}

export interface AiConversation {
  id: string
  shopId?: string
  createdAt?: string
  messages: AiMessageView[]
}

export interface AiKnowledgeDoc {
  id: string
  seedKey?: string
  scope: string
  shopId?: string
  title: string
  body: string
  status: string
  publishedVersion?: number
  epoch: number
  disabled: boolean
  latestVersion?: number
  latestStatus?: string
  latestError?: string
  updateTime?: string
}

export interface PublicKnowledge {
  documentId: string
  version: number
  scope: string
  shopId?: string
  title: string
  body: string
  epoch: number
}
