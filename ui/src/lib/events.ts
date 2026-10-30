/**
 * Event types that invalidate dashboard inventory. Database and Redis
 * mutations refresh the service/site views through the same signal as the
 * existing site/service events.
 */
export function isRefreshEvent(type: string): boolean {
  return /^(site|runtime|doctor|trust|service|database|redis)\./.test(type);
}
