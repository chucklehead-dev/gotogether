module __gtg {
  const __gtg_ACCOUNTS: Set[str]
  const __gtg_JOINT: Set[str]
  const __gtg_INITIAL: int
  import wallet(ACCOUNTS = __gtg_ACCOUNTS, JOINT = __gtg_JOINT, INITIAL = __gtg_INITIAL).* from "./wallet"
  def __gtg_expect_transfer(p_id: int, p_obs_ok: bool): bool = result.get(p_id) == p_obs_ok
  val __gtg_inv_0 = noOverdraft
  val __gtg_ev_0 = allPosted
  action __gtg_act_transfer(id: int, src: str, dest: str, amount: int): bool = transfer(id, src, dest, amount)
  action __gtg_init = init
  action __gtg_step = all { __gtg_inv_0 or __gtg_ev_0 or __gtg_expect_transfer(1, true), __gtg_act_transfer(1, "a", "b", 1) }
}
