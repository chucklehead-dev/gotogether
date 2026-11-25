// imports from another file: quint parses it after this one, with the
// same ids, and so must the Go parse
module imports {
  import shared(K = 1) as S from "./lib/shared"
  import shared.* from "lib/shared"
  val one = S::inc(0)
}
