// Copyright 2020 ConsenSys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by gnark DO NOT EDIT

package plonk

import (
	"errors"
	"github.com/consensys/gnark-crypto/ecc/bw6-633/fr"
	"github.com/consensys/gnark-crypto/ecc/bw6-633/fr/fft"
	"github.com/consensys/gnark-crypto/ecc/bw6-633/fr/iop"
	"github.com/consensys/gnark-crypto/ecc/bw6-633/fr/kzg"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/constraint/bw6-633"

	kzgg "github.com/consensys/gnark-crypto/kzg"
)

// ProvingKey stores the data needed to generate a proof:
// * the commitment scheme
// * ql, prepended with as many ones as they are public inputs
// * qr, qm, qo prepended with as many zeroes as there are public inputs.
// * qk, prepended with as many zeroes as public inputs, to be completed by the prover
// with the list of public inputs.
// * sigma_1, sigma_2, sigma_3 in both basis
// * the copy constraint permutation
type ProvingKey struct {
	// Verifying Key is embedded into the proving key (needed by Prove)
	Vk *VerifyingKey

	// TODO store iop.Polynomial here, not []fr.Element for more "type safety"

	// qr,ql,qm,qo,qcp (in canonical basis).
	// QcPrime denotes the constraints defining committed variables
	Ql, Qr, Qm, Qo, QcPrime []fr.Element

	// qr,ql,qm,qo,qcp (in lagrange coset basis) --> these are not serialized, but computed from Ql, Qr, Qm, Qo once.
	lQl, lQr, lQm, lQo, lQcPrime []fr.Element

	// LQk (CQk) qk in Lagrange basis (canonical basis), prepended with as many zeroes as public inputs.
	// Storing LQk in Lagrange basis saves a fft...
	CQk, LQk []fr.Element

	// Domains used for the FFTs.
	// Domain[0] = small Domain
	// Domain[1] = big Domain
	Domain [2]fft.Domain
	// Domain[0], Domain[1] fft.Domain

	// Permutation polynomials
	S1Canonical, S2Canonical, S3Canonical []fr.Element

	// in lagrange coset basis --> these are not serialized, but computed from S1Canonical, S2Canonical, S3Canonical once.
	lS1LagrangeCoset, lS2LagrangeCoset, lS3LagrangeCoset []fr.Element

	// position -> permuted position (position in [0,3*sizeSystem-1])
	Permutation []int64
}

// VerifyingKey stores the data needed to verify a proof:
// * The commitment scheme
// * Commitments of ql prepended with as many ones as there are public inputs
// * Commitments of qr, qm, qo, qk prepended with as many zeroes as there are public inputs
// * Commitments to S1, S2, S3
type VerifyingKey struct {
	// Size circuit
	Size              uint64
	SizeInv           fr.Element
	Generator         fr.Element
	NbPublicVariables uint64

	// Commitment scheme that is used for an instantiation of PLONK
	KZGSRS *kzg.SRS

	// cosetShift generator of the coset on the small domain
	CosetShift fr.Element

	// S commitments to S1, S2, S3
	S [3]kzg.Digest

	// Commitments to ql, qr, qm, qo prepended with as many zeroes (ones for l) as there are public inputs.
	// In particular Qk is not complete.
	Ql, Qr, Qm, Qo, Qk, QcPrime kzg.Digest

	CommitmentInfo constraint.Commitment
}

// Setup sets proving and verifying keys
func Setup(spr *cs.SparseR1CS, srs *kzg.SRS) (*ProvingKey, *VerifyingKey, error) {
	var pk ProvingKey
	var vk VerifyingKey

	// The verifying key shares data with the proving key
	pk.Vk = &vk
	vk.CommitmentInfo = spr.CommitmentInfo

	nbConstraints := len(spr.Constraints)

	// fft domains
	sizeSystem := uint64(nbConstraints + len(spr.Public)) // len(spr.Public) is for the placeholder constraints
	pk.Domain[0] = *fft.NewDomain(sizeSystem)
	pk.Vk.CosetShift.Set(&pk.Domain[0].FrMultiplicativeGen)

	// h, the quotient polynomial is of degree 3(n+1)+2, so it's in a 3(n+2) dim vector space,
	// the domain is the next power of 2 superior to 3(n+2). 4*domainNum is enough in all cases
	// except when n<6.
	if sizeSystem < 6 {
		pk.Domain[1] = *fft.NewDomain(8 * sizeSystem)
	} else {
		pk.Domain[1] = *fft.NewDomain(4 * sizeSystem)
	}

	vk.Size = pk.Domain[0].Cardinality
	vk.SizeInv.SetUint64(vk.Size).Inverse(&vk.SizeInv)
	vk.Generator.Set(&pk.Domain[0].Generator)
	vk.NbPublicVariables = uint64(len(spr.Public))

	if err := pk.InitKZG(srs); err != nil {
		return nil, nil, err
	}

	// public polynomials corresponding to constraints: [ placeholders | constraints | assertions ]
	pk.Ql = make([]fr.Element, pk.Domain[0].Cardinality)
	pk.Qr = make([]fr.Element, pk.Domain[0].Cardinality)
	pk.Qm = make([]fr.Element, pk.Domain[0].Cardinality)
	pk.QcPrime = make([]fr.Element, pk.Domain[0].Cardinality)
	pk.Qo = make([]fr.Element, pk.Domain[0].Cardinality)
	pk.CQk = make([]fr.Element, pk.Domain[0].Cardinality)
	pk.LQk = make([]fr.Element, pk.Domain[0].Cardinality)

	for i := 0; i < len(spr.Public); i++ { // placeholders (-PUB_INPUT_i + qk_i = 0) TODO should return error is size is inconsistent
		pk.Ql[i].SetOne().Neg(&pk.Ql[i])
		pk.Qr[i].SetZero()
		pk.Qm[i].SetZero()
		pk.Qo[i].SetZero()
		pk.CQk[i].SetZero()
		pk.LQk[i].SetZero() // → to be completed by the prover
	}
	offset := len(spr.Public)
	for i := 0; i < nbConstraints; i++ { // constraints

		pk.Ql[offset+i].Set(&spr.Coefficients[spr.Constraints[i].L.CoeffID()])
		pk.Qr[offset+i].Set(&spr.Coefficients[spr.Constraints[i].R.CoeffID()])
		pk.Qm[offset+i].Set(&spr.Coefficients[spr.Constraints[i].M[0].CoeffID()]).
			Mul(&pk.Qm[offset+i], &spr.Coefficients[spr.Constraints[i].M[1].CoeffID()])
		pk.Qo[offset+i].Set(&spr.Coefficients[spr.Constraints[i].O.CoeffID()])
		pk.CQk[offset+i].Set(&spr.Coefficients[spr.Constraints[i].K])
		pk.LQk[offset+i].Set(&spr.Coefficients[spr.Constraints[i].K])
	}

	for _, committed := range spr.CommitmentInfo.Committed {
		pk.QcPrime[committed].SetOne()
	}

	pk.Domain[0].FFTInverse(pk.Ql, fft.DIF)
	pk.Domain[0].FFTInverse(pk.Qr, fft.DIF)
	pk.Domain[0].FFTInverse(pk.Qm, fft.DIF)
	pk.Domain[0].FFTInverse(pk.Qo, fft.DIF)
	pk.Domain[0].FFTInverse(pk.CQk, fft.DIF)
	pk.Domain[0].FFTInverse(pk.QcPrime, fft.DIF)
	fft.BitReverse(pk.Ql)
	fft.BitReverse(pk.Qr)
	fft.BitReverse(pk.Qm)
	fft.BitReverse(pk.Qo)
	fft.BitReverse(pk.CQk)
	fft.BitReverse(pk.QcPrime)

	// build permutation. Note: at this stage, the permutation takes in account the placeholders
	buildPermutation(spr, &pk)

	// set s1, s2, s3
	ccomputePermutationPolynomials(&pk)

	// compute the lagrange coset basis versions (not serialized)
	pk.computeLagrangeCosetPolys()

	// Commit to the polynomials to set up the verifying key
	var err error
	if vk.Ql, err = kzg.Commit(pk.Ql, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qr, err = kzg.Commit(pk.Qr, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qm, err = kzg.Commit(pk.Qm, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.QcPrime, err = kzg.Commit(pk.QcPrime, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qo, err = kzg.Commit(pk.Qo, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.Qk, err = kzg.Commit(pk.CQk, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.S[0], err = kzg.Commit(pk.S1Canonical, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.S[1], err = kzg.Commit(pk.S2Canonical, vk.KZGSRS); err != nil {
		return nil, nil, err
	}
	if vk.S[2], err = kzg.Commit(pk.S3Canonical, vk.KZGSRS); err != nil {
		return nil, nil, err
	}

	return &pk, &vk, nil

}

// buildPermutation builds the Permutation associated with a circuit.
//
// The permutation s is composed of cycles of maximum length such that
//
//	s. (l∥r∥o) = (l∥r∥o)
//
// , where l∥r∥o is the concatenation of the indices of l, r, o in
// ql.l+qr.r+qm.l.r+qo.O+k = 0.
//
// The permutation is encoded as a slice s of size 3*size(l), where the
// i-th entry of l∥r∥o is sent to the s[i]-th entry, so it acts on a tab
// like this: for i in tab: tab[i] = tab[permutation[i]]
func buildPermutation(spr *cs.SparseR1CS, pk *ProvingKey) {

	nbVariables := spr.NbInternalVariables + len(spr.Public) + len(spr.Secret)
	sizeSolution := int(pk.Domain[0].Cardinality)

	// init permutation
	pk.Permutation = make([]int64, 3*sizeSolution)
	for i := 0; i < len(pk.Permutation); i++ {
		pk.Permutation[i] = -1
	}

	// init LRO position -> variable_ID
	lro := make([]int, 3*sizeSolution) // position -> variable_ID
	for i := 0; i < len(spr.Public); i++ {
		lro[i] = i // IDs of LRO associated to placeholders (only L needs to be taken care of)
	}

	offset := len(spr.Public)
	for i := 0; i < len(spr.Constraints); i++ { // IDs of LRO associated to constraints
		lro[offset+i] = spr.Constraints[i].L.WireID()
		lro[sizeSolution+offset+i] = spr.Constraints[i].R.WireID()
		lro[2*sizeSolution+offset+i] = spr.Constraints[i].O.WireID()
	}

	// init cycle:
	// map ID -> last position the ID was seen
	cycle := make([]int64, nbVariables)
	for i := 0; i < len(cycle); i++ {
		cycle[i] = -1
	}

	for i := 0; i < len(lro); i++ {
		if cycle[lro[i]] != -1 {
			// if != -1, it means we already encountered this value
			// so we need to set the corresponding permutation index.
			pk.Permutation[i] = cycle[lro[i]]
		}
		cycle[lro[i]] = int64(i)
	}

	// complete the Permutation by filling the first IDs encountered
	for i := 0; i < len(pk.Permutation); i++ {
		if pk.Permutation[i] == -1 {
			pk.Permutation[i] = cycle[lro[i]]
		}
	}
}

func (pk *ProvingKey) computeLagrangeCosetPolys() {
	canReg := iop.Form{Basis: iop.Canonical, Layout: iop.Regular}
	wqliop := iop.NewPolynomial(clone(pk.Ql, pk.Domain[1].Cardinality), canReg)
	wqriop := iop.NewPolynomial(clone(pk.Qr, pk.Domain[1].Cardinality), canReg)
	wqmiop := iop.NewPolynomial(clone(pk.Qm, pk.Domain[1].Cardinality), canReg)
	wqoiop := iop.NewPolynomial(clone(pk.Qo, pk.Domain[1].Cardinality), canReg)
	wqcpiop := iop.NewPolynomial(clone(pk.QcPrime, pk.Domain[1].Cardinality), canReg)

	ws1 := iop.NewPolynomial(clone(pk.S1Canonical, pk.Domain[1].Cardinality), canReg)
	ws2 := iop.NewPolynomial(clone(pk.S2Canonical, pk.Domain[1].Cardinality), canReg)
	ws3 := iop.NewPolynomial(clone(pk.S3Canonical, pk.Domain[1].Cardinality), canReg)

	wqliop.ToLagrangeCoset(&pk.Domain[1])
	wqriop.ToLagrangeCoset(&pk.Domain[1])
	wqmiop.ToLagrangeCoset(&pk.Domain[1])
	wqoiop.ToLagrangeCoset(&pk.Domain[1])
	wqcpiop.ToLagrangeCoset(&pk.Domain[1])

	ws1.ToLagrangeCoset(&pk.Domain[1])
	ws2.ToLagrangeCoset(&pk.Domain[1])
	ws3.ToLagrangeCoset(&pk.Domain[1])

	pk.lQl = wqliop.Coefficients()
	pk.lQr = wqriop.Coefficients()
	pk.lQm = wqmiop.Coefficients()
	pk.lQo = wqoiop.Coefficients()
	pk.lQcPrime = wqcpiop.Coefficients()

	pk.lS1LagrangeCoset = ws1.Coefficients()
	pk.lS2LagrangeCoset = ws2.Coefficients()
	pk.lS3LagrangeCoset = ws3.Coefficients()
}

func clone(input []fr.Element, capacity uint64) *[]fr.Element {
	res := make([]fr.Element, len(input), capacity)
	copy(res, input)
	return &res
}

// ccomputePermutationPolynomials computes the LDE (Lagrange basis) of the permutations
// s1, s2, s3.
//
// 1	z 	..	z**n-1	|	u	uz	..	u*z**n-1	|	u**2	u**2*z	..	u**2*z**n-1  |
//
//																						 |
//	      																				 | Permutation
//
// s11  s12 ..   s1n	   s21 s22 	 ..		s2n		     s31 	s32 	..		s3n		 v
// \---------------/       \--------------------/        \------------------------/
//
//	s1 (LDE)                s2 (LDE)                          s3 (LDE)
func ccomputePermutationPolynomials(pk *ProvingKey) {

	nbElmts := int(pk.Domain[0].Cardinality)

	// Lagrange form of ID
	evaluationIDSmallDomain := getIDSmallDomain(&pk.Domain[0])

	// Lagrange form of S1, S2, S3
	pk.S1Canonical = make([]fr.Element, nbElmts)
	pk.S2Canonical = make([]fr.Element, nbElmts)
	pk.S3Canonical = make([]fr.Element, nbElmts)
	for i := 0; i < nbElmts; i++ {
		pk.S1Canonical[i].Set(&evaluationIDSmallDomain[pk.Permutation[i]])
		pk.S2Canonical[i].Set(&evaluationIDSmallDomain[pk.Permutation[nbElmts+i]])
		pk.S3Canonical[i].Set(&evaluationIDSmallDomain[pk.Permutation[2*nbElmts+i]])
	}

	// Canonical form of S1, S2, S3
	pk.Domain[0].FFTInverse(pk.S1Canonical, fft.DIF)
	pk.Domain[0].FFTInverse(pk.S2Canonical, fft.DIF)
	pk.Domain[0].FFTInverse(pk.S3Canonical, fft.DIF)
	fft.BitReverse(pk.S1Canonical)
	fft.BitReverse(pk.S2Canonical)
	fft.BitReverse(pk.S3Canonical)
}

// getIDSmallDomain returns the Lagrange form of ID on the small domain
func getIDSmallDomain(domain *fft.Domain) []fr.Element {

	res := make([]fr.Element, 3*domain.Cardinality)

	res[0].SetOne()
	res[domain.Cardinality].Set(&domain.FrMultiplicativeGen)
	res[2*domain.Cardinality].Square(&domain.FrMultiplicativeGen)

	for i := uint64(1); i < domain.Cardinality; i++ {
		res[i].Mul(&res[i-1], &domain.Generator)
		res[domain.Cardinality+i].Mul(&res[domain.Cardinality+i-1], &domain.Generator)
		res[2*domain.Cardinality+i].Mul(&res[2*domain.Cardinality+i-1], &domain.Generator)
	}

	return res
}

// InitKZG inits pk.Vk.KZG using pk.Domain[0] cardinality and provided SRS
//
// This should be used after deserializing a ProvingKey
// as pk.Vk.KZG is NOT serialized
func (pk *ProvingKey) InitKZG(srs kzgg.SRS) error {
	return pk.Vk.InitKZG(srs)
}

// InitKZG inits vk.KZG using provided SRS
//
// This should be used after deserializing a VerifyingKey
// as vk.KZG is NOT serialized
//
// Note that this instantiates a new FFT domain using vk.Size
func (vk *VerifyingKey) InitKZG(srs kzgg.SRS) error {
	_srs := srs.(*kzg.SRS)

	if len(_srs.G1) < int(vk.Size) {
		return errors.New("kzg srs is too small")
	}
	vk.KZGSRS = _srs

	return nil
}

// NbPublicWitness returns the expected public witness size (number of field elements)
func (vk *VerifyingKey) NbPublicWitness() int {
	return int(vk.NbPublicVariables)
}

// VerifyingKey returns pk.Vk
func (pk *ProvingKey) VerifyingKey() interface{} {
	return pk.Vk
}
