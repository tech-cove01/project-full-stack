export interface Result<T> {
  success: boolean
  errorMsg?: string
  data: T
  total?: number
}

export interface User {
  id: string
  nickName: string
  icon?: string
}

export interface Shop {
  id: string
  name: string
  images?: string
  area?: string
  address?: string
  avgPrice?: number | string
  sold?: number
  comments?: number
  score?: number
  openHours?: string
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

export interface OrderStatus {
  id: string
  state: 'PROCESSING' | 'CREATED' | 'FAILED'
  voucherId?: string
  failureReason?: string
  errorMsg?: string
  message?: string
}

export interface MerchantWorkspace {
  merchants: {
    id: string
    name: string
    role: 'OWNER' | 'MANAGER' | 'STAFF'
    shops: { id: string; name: string; address: string }[]
  }[]
}
