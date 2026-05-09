package main

func main() {
	var (
		env        = NewEnv()
		stdDepends = LithpModule{
			Name:     "stdDependencies",
			Children: nil,
			Data:     stdDependencies{},
		}
	)

	Expose(&stdDepends)

	activeEnv = &env

	PushFrame()

	lithpImport := (*env.Globals)["import"].Val.Val.(tfun)

	lithpImport([]ttnode{ttleaf("\"main\"")})

	PopFrame()
}
