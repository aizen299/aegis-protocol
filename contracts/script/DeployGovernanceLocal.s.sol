// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {ERC1967Proxy} from "@openzeppelin/contracts/proxy/ERC1967/ERC1967Proxy.sol";
import {Script} from "forge-std/Script.sol";
import {console2} from "forge-std/console2.sol";

import {AegisToken} from "../src/governance/AegisToken.sol";
import {Governor} from "../src/governance/Governor.sol";
import {Timelock} from "../src/governance/Timelock.sol";
import {Roles} from "../src/shared/access/Roles.sol";
import {GovernedTarget} from "../test/utils/GovernorFixture.sol";

/// @notice Local-only governance deployment for development and the end-to-end test.
/// @dev Never run this against a public network: it grants every role to one key. Parameters are
///      the real ones from docs/v0.3-governance-plan.md §2.5 — a short-cycle configuration would
///      test something that never ships.
contract DeployGovernanceLocal is Script {
    uint256 internal constant SUPPLY = 100_000_000e18;
    uint48 internal constant VOTING_DELAY = 1 days;
    uint48 internal constant VOTING_PERIOD = 7 days;
    uint48 internal constant TIMELOCK_DELAY = 2 days;
    uint256 internal constant PROPOSAL_THRESHOLD = SUPPLY / 100;
    uint256 internal constant QUORUM_NUMERATOR = 4;

    function run()
        external
        returns (address token, address timelockProxy, address governorProxy, address target)
    {
        uint256 deployerKey = vm.envUint("PRIVATE_KEY");
        address deployer = vm.addr(deployerKey);

        vm.startBroadcast(deployerKey);

        token = address(new AegisToken(deployer, SUPPLY));
        target = address(new GovernedTarget());

        timelockProxy = address(
            new ERC1967Proxy(
                address(new Timelock()),
                abi.encodeCall(Timelock.initialize, (deployer, TIMELOCK_DELAY))
            )
        );

        governorProxy = address(
            new ERC1967Proxy(
                address(new Governor()),
                abi.encodeCall(
                    Governor.initialize,
                    (
                        deployer,
                        token,
                        timelockProxy,
                        VOTING_DELAY,
                        VOTING_PERIOD,
                        PROPOSAL_THRESHOLD,
                        QUORUM_NUMERATOR
                    )
                )
            )
        );

        // The governor is the timelock's only client. Granting these anywhere else would let an
        // action run while its proposal still read `queued`.
        Timelock(payable(timelockProxy)).grantRole(Roles.TIMELOCK_PROPOSER_ROLE, governorProxy);
        Timelock(payable(timelockProxy)).grantRole(Roles.TIMELOCK_EXECUTOR_ROLE, governorProxy);
        Timelock(payable(timelockProxy)).grantRole(Roles.TIMELOCK_CANCELLER_ROLE, governorProxy);

        Governor(governorProxy).grantRole(Roles.GOVERNANCE_GUARDIAN_ROLE, deployer);
        Governor(governorProxy).grantRole(Roles.UPGRADER_ROLE, deployer);
        Timelock(payable(timelockProxy)).grantRole(Roles.UPGRADER_ROLE, deployer);

        vm.stopBroadcast();

        _writeArtifact(token, timelockProxy, governorProxy, target);

        console2.log("AegisToken:     ", token);
        console2.log("Timelock:       ", timelockProxy);
        console2.log("Governor:       ", governorProxy);
        console2.log("GovernedTarget: ", target);
    }

    function _writeArtifact(
        address token,
        address timelockProxy,
        address governorProxy,
        address target
    ) internal {
        string memory key = "governance";
        vm.serializeAddress(key, "aegisToken", token);
        vm.serializeAddress(key, "timelock", timelockProxy);
        vm.serializeAddress(key, "governor", governorProxy);
        vm.serializeAddress(key, "governedTarget", target);
        vm.serializeUint(key, "chainId", block.chainid);
        string memory out = vm.serializeUint(key, "deployedAtBlock", block.number);

        vm.writeJson(
            out, string.concat("./deployments/governance-", vm.toString(block.chainid), ".json")
        );
    }
}
