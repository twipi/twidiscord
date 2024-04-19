{ pkgs ? import <nixpkgs> {} }:

let
	lib = pkgs.lib;
in

pkgs.mkShell {
	name = "twidiscord";

	buildInputs = with pkgs; [
		go_1_22
		gopls
		gotools
		sqlc
	];

	TWIDISCORD_DEBUG = "";
}
